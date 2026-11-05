// Package installation builds node installation artifacts.
package installation

import (
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/deploy/apt"
	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// Artifacts builds installation artifacts for a node.
type Artifacts struct{}

// Build assembles the artifact.
func (Artifacts) Build(spec panel.InstallSpec) (panel.InstallArtifact, error) {
	c, name := spec.Cert, spec.Name

	panelAddr := c.PanelAddr
	if panelAddr == "" {
		panelAddr = "<panel-host>:9443"
	}

	fw := firewallSpec(spec.Inbounds)

	cmd := "./install-agent.sh --panel " + shellQuote(panelAddr) + " --repo " + shellQuote(spec.Repo)
	if spec.Firewall && fw != "" {
		cmd += " --firewall " + shellQuote(fw)
		if spec.SSHPort > 0 && spec.SSHPort != 22 {
			cmd += fmt.Sprintf(" --ssh-port %d", spec.SSHPort)
		}
	}

	readme := fmt.Sprintf(`Installing the agent for node %[1]s (Debian/Ubuntu, amd64 or arm64)

1. Copy the archive to the node and unpack it:
     scp %[1]s-agent.tar.gz root@<node>:
     ssh root@<node> 'tar xzf %[1]s-agent.tar.gz'
2. On the node as root:
     cd %[1]s && ./install-agent.sh --panel %[2]s --repo %[3]s
   The script adds the apt repository, installs giocondo-vpn-agent and giocondo-vpn-sing-box,
   puts the certificates into /etc/giocondo-vpn/agent and the panel address into /etc/giocondo-vpn/agent.env.
   Without repository access: --deb giocondo-vpn-agent_*.deb --deb giocondo-vpn-sing-box_*.deb.
   Close everything except SSH and the node protocol ports (ufw): --firewall '%[4]s'.
3. Logs: journalctl -u giocondo-vpn-agent -f. The node becomes online in the panel.

agent.key is a secret: do not send it in plain text.
`, name, panelAddr, spec.Repo, fw)
	files := []panel.InstallFile{
		{Path: "ca.crt", Mode: 0o644, Data: []byte(c.CACert)},
		{Path: "agent.crt", Mode: 0o644, Data: []byte(c.AgentCert)},
		{Path: "agent.key", Mode: 0o600, Data: []byte(c.AgentKey)},
		{Path: "README.txt", Mode: 0o644, Data: []byte(readme)},
	}

	err := fs.WalkDir(apt.Files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		data, err := apt.Files.ReadFile(path)
		if err != nil {
			return err
		}

		mode := int64(0o644)
		if strings.HasSuffix(path, ".sh") {
			mode = 0o755
		}

		files = append(files, panel.InstallFile{Path: path, Mode: mode, Data: data})

		return nil
	})
	if err != nil {
		return panel.InstallArtifact{}, err
	}

	if len(spec.ACMEState) > 0 {
		files = append(files, panel.InstallFile{Path: "acme.tar.gz", Mode: 0600, Data: spec.ACMEState})
		cmd = "mkdir -p /var/lib/giocondo-vpn/sing-box/acme && " +
			"tar xzf acme.tar.gz -C /var/lib/giocondo-vpn/sing-box/acme && " + cmd
	}

	return panel.InstallArtifact{Name: name, Files: files, Command: cmd}, nil
}

// firewallSpec lists the node protocol ports for `install-agent.sh --firewall`: VLESS over TCP,
// Hysteria2 and WireGuard over UDP; Hysteria2 with Let's Encrypt also needs 80/tcp (HTTP-01).
func firewallSpec(ins []protocol.Inbound) string {
	var out []string

	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}

	for _, in := range ins {
		switch in.Type {
		case protocol.TypeVLESSReality:
			add(fmt.Sprintf("%d/tcp", in.Port))
		case protocol.TypeHysteria2:
			add(fmt.Sprintf("%d/udp", in.Port))

			if in.Hysteria2 != nil && in.Hysteria2.TLSMode() == protocol.TLSACME {
				add("80/tcp")
			}
		case protocol.TypeWireGuard:
			add(fmt.Sprintf("%d/udp", in.Port))
		}
	}

	return strings.Join(out, ",")
}

// shellQuote quotes an sh argument in single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func artifactFiles(a panel.InstallArtifact) []File {
	files := make([]File, len(a.Files))
	for i, f := range a.Files {
		files[i] = File{Path: f.Path, Mode: f.Mode, Data: f.Data}
	}

	return files
}

// Archive packages an artifact.
func (Artifacts) Archive(a panel.InstallArtifact, at time.Time) ([]byte, error) {
	return TarGz(a.Name, artifactFiles(a), at)
}

// CloudInit renders cloud-init user data.
func (Artifacts) CloudInit(a panel.InstallArtifact) ([]byte, error) {
	return CloudInit(artifactFiles(a), a.Command)
}
