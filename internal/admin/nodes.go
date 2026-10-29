package admin

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/deploy/apt"
	"github.com/artemlavr/giocondo/internal/nodeapi/pki"
	"github.com/artemlavr/giocondo/internal/provision"
	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
)

// InboundView is a node protocol in a list.
type InboundView struct {
	Tag  string              `json:"tag"`
	Type singbox.InboundType `json:"type"`
	Port uint16              `json:"port"`
}

// NodeView is a node for the API and pages.
type NodeView struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	IP      string `json:"ip"`
	Region  string `json:"region"`
	Status  string `json:"status"`
	Runtime string `json:"runtime"` // remote | local
	// Dirty means the node runs a different config than the panel built (pending or failed to apply).
	Dirty          bool                     `json:"dirty"`
	ApplyError     string                   `json:"apply_error"` // error of the last apply on the node
	LastSeen       *time.Time               `json:"last_seen"`
	CreatedAt      time.Time                `json:"created_at"`
	Inbounds       []InboundView            `json:"inbounds"`
	Hosts          []HostView               `json:"hosts"`
	ManagedDNS     bool                     `json:"managed_dns"`
	Addresses      []store.Address          `json:"addresses"`
	Probes         []store.ProbeResult      `json:"probes"`
	Warning        bool                     `json:"warning"`
	AgentConnected bool                     `json:"agent_connected"`
	DNSPreview     *DNSPreview              `json:"dns_preview,omitempty"`
	Operations     []store.NetworkOperation `json:"operations"`
}

func (s *Service) nodeView(ctx context.Context, n store.Node) (NodeView, error) {
	ins, err := s.Store.ListInbounds(ctx, n.ID)
	if err != nil {
		return NodeView{}, err
	}
	v := NodeView{ID: n.ID, Name: n.Name, Host: n.Host, IP: n.IP, Region: n.Region, Status: string(n.Status),
		Runtime: string(n.Runtime), Dirty: n.Dirty(), ApplyError: n.ApplyError,
		LastSeen: n.LastSeen, CreatedAt: n.CreatedAt, Inbounds: []InboundView{}}
	for _, in := range ins {
		v.Inbounds = append(v.Inbounds, InboundView{Tag: in.Spec.Tag, Type: in.Spec.Type, Port: in.Spec.Port})
	}
	if v.Hosts, err = s.ListHosts(ctx, n.ID); err != nil {
		return NodeView{}, err
	}
	v.ManagedDNS = n.ManagedDNS
	v.Warning = n.DownNotified
	v.AgentConnected = n.LastSeen != nil && s.now().Sub(*n.LastSeen) < 2*time.Minute && n.Status == store.NodeOnline
	if v.Addresses, err = s.Store.ListAddresses(ctx, n.ID); err != nil {
		return NodeView{}, err
	}
	if v.Probes, err = s.Store.ListProbes(ctx, n.ID); err != nil {
		return NodeView{}, err
	}
	if v.Operations, err = s.Store.ListNetworkOperations(ctx, n.ID); err != nil {
		return NodeView{}, err
	}
	if preview, e := s.PreviewDNS(ctx, n.ID); e == nil {
		v.DNSPreview = &preview
	}
	return v, nil
}

// ListNodes returns nodes ordered by id.
func (s *Service) ListNodes(ctx context.Context) ([]NodeView, error) {
	nodes, err := s.Store.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]NodeView, 0, len(nodes))
	for _, n := range nodes {
		v, err := s.nodeView(ctx, n)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// GetNode returns a node.
func (s *Service) GetNode(ctx context.Context, id int64) (NodeView, error) {
	n, err := s.Store.GetNode(ctx, id)
	if err != nil {
		return NodeView{}, err
	}
	return s.nodeView(ctx, *n)
}

// CreateNodeInput is a new node. A protocol is enabled if its block is not nil; empty fields
// get default values, keys and certificates are generated.
type CreateNodeInput struct {
	Name      string       `json:"name"`
	Host      string       `json:"host"`
	IP        string       `json:"ip"`
	Region    string       `json:"region"`
	VLESS     *VLESSInput  `json:"vless_reality"`
	Hysteria2 *Hy2Input    `json:"hysteria2"`
	WireGuard *WGNodeInput `json:"wireguard"`
	// Runtime is remote (default) or local; only `panel local-node` creates local.
	Runtime store.NodeRuntime `json:"-"`
}

// VLESSInput holds VLESS Reality parameters.
type VLESSInput struct {
	Port       uint16 `json:"port"`        // 443
	Dest       string `json:"dest"`        // www.microsoft.com:443
	ServerName string `json:"server_name"` // www.microsoft.com
}

// Hy2Input holds Hysteria2 parameters.
type Hy2Input struct {
	Port      uint16 `json:"port"` // 8443
	ACME      bool   `json:"acme"` // Let's Encrypt certificate instead of a self-signed one (host must be a domain)
	ACMEEmail string `json:"acme_email"`
	Obfs      bool   `json:"obfs"` // salamander with a random password
}

// WGNodeInput holds WireGuard parameters.
type WGNodeInput struct {
	Port uint16 `json:"port"` // 51820
}

// CreateNode registers a node with protocols. The agent then needs a certificate (AgentBundle).
func (s *Service) CreateNode(ctx context.Context, in CreateNodeInput) (NodeView, error) {
	in.Name, in.Host = strings.TrimSpace(in.Name), strings.TrimSpace(in.Host)
	if !singbox.ValidUserName(in.Name) {
		return NodeView{}, invalid("node name %q: use latin letters, digits, '.', '_' or '-'", in.Name)
	}
	if in.Host == "" {
		return NodeView{}, invalid("host is required (domain or IP clients connect to)")
	}
	spec := singbox.Node{ID: in.Name, Host: in.Host}
	if v := in.VLESS; v != nil {
		p := &singbox.RealityParams{Dest: v.Dest, ServerName: v.ServerName}
		if p.Dest == "" {
			p.Dest = "www.microsoft.com:443"
		}
		if p.ServerName == "" {
			p.ServerName, _, _ = strings.Cut(p.Dest, ":")
		}
		spec.Inbounds = append(spec.Inbounds, singbox.Inbound{Type: singbox.TypeVLESSReality, Tag: "vless", Port: orPort(v.Port, 443), Reality: p})
	}
	if h := in.Hysteria2; h != nil {
		p := &singbox.Hysteria2Params{}
		if h.ACME {
			p.TLS, p.ACMEEmail = singbox.TLSACME, h.ACMEEmail
		}
		if h.Obfs {
			pw, err := singbox.NewPresharedKey()
			if err != nil {
				return NodeView{}, err
			}
			p.ObfsPassword = strings.TrimRight(pw, "=")
		}
		spec.Inbounds = append(spec.Inbounds, singbox.Inbound{Type: singbox.TypeHysteria2, Tag: "hy2", Port: orPort(h.Port, 8443), Hysteria2: p})
	}
	if w := in.WireGuard; w != nil {
		spec.Inbounds = append(spec.Inbounds, singbox.Inbound{Type: singbox.TypeWireGuard, Tag: "wg", Port: orPort(w.Port, 51820),
			WireGuard: &singbox.WireGuardParams{}})
	}
	if len(spec.Inbounds) == 0 {
		return NodeView{}, invalid("enable at least one protocol")
	}
	if err := singbox.Fill(&spec, nil); err != nil {
		return NodeView{}, err
	}
	if err := singbox.Validate(&spec, nil); err != nil {
		return NodeView{}, invalid("%v", err)
	}

	n := store.Node{Name: in.Name, Host: in.Host, IP: strings.TrimSpace(in.IP), Region: strings.TrimSpace(in.Region),
		Status: store.NodeNew, Runtime: in.Runtime}
	if err := s.Store.CreateNode(ctx, &n); err != nil {
		return NodeView{}, err
	}
	for _, ib := range spec.Inbounds {
		if err := s.Store.CreateInbound(ctx, &store.Inbound{NodeID: n.ID, Spec: ib}); err != nil {
			_ = s.Store.DeleteNode(ctx, n.ID) // no transaction: do not leave a half-created node
			return NodeView{}, err
		}
	}
	if err := s.ensureWG(ctx); err != nil {
		s.log().Error("issue wireguard keys", "err", err)
	}
	if s.Checker != nil {
		if _, err := s.EnsureMonitor(ctx, n.ID); err != nil {
			return NodeView{}, err
		}
	}
	s.log().Info("node created", "node", n.Name, "host", n.Host)
	s.kick() // ensureWG may have issued keys: other WG nodes need new peers
	return s.GetNode(ctx, n.ID)
}

func orPort(p, def uint16) uint16 {
	if p == 0 {
		return def
	}
	return p
}

// UpdateNodeInput is a partial node change. Disabled=true removes the node from subscriptions
// and disconnects its agent; false brings it back.
type UpdateNodeInput struct {
	Host     *string `json:"host"`
	IP       *string `json:"ip"`
	Region   *string `json:"region"`
	Disabled *bool   `json:"disabled"`
}

// UpdateNode changes the address, IP, region and enabled state.
func (s *Service) UpdateNode(ctx context.Context, id int64, in UpdateNodeInput) (NodeView, error) {
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	n, err := s.Store.GetNode(ctx, id)
	if err != nil {
		return NodeView{}, err
	}
	if err := s.unlocked(ctx, id); err != nil {
		return NodeView{}, err
	}
	if n.ManagedDNS && ((in.Host != nil && *in.Host != n.Host) || (in.IP != nil && *in.IP != n.IP)) {
		return NodeView{}, invalid("use rotate-ip for managed node addresses")
	}
	if in.Host != nil {
		h := strings.TrimSpace(*in.Host)
		if h == "" {
			return NodeView{}, invalid("host must not be empty")
		}
		n.Host = h
	}
	if in.IP != nil {
		n.IP = strings.TrimSpace(*in.IP)
	}
	if in.Region != nil {
		n.Region = strings.TrimSpace(*in.Region)
	}
	if in.Disabled != nil {
		switch {
		case *in.Disabled:
			n.Status = store.NodeDisabled
		case n.Status == store.NodeDisabled:
			n.Status = store.NodeOffline // the agent reconnects and becomes online
		}
	}
	if err := s.Store.UpdateNode(ctx, n); err != nil {
		return NodeView{}, err
	}
	s.log().Info("node updated", "node", n.Name, "status", n.Status)
	s.kick()
	return s.GetNode(ctx, id)
}

// DeleteNode deletes a node, its protocols and traffic. The VPS at the provider is not touched.
func (s *Service) DeleteNode(ctx context.Context, id int64) error {
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	if err := s.unlocked(ctx, id); err != nil {
		return err
	}
	n, err := s.Store.GetNode(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteNode(ctx, id); err != nil {
		return err
	}
	s.log().Info("node deleted", "node", n.Name)
	s.kick()
	return nil
}

// AgentCert holds the certificates for a node agent.
type AgentCert struct {
	CACert    string `json:"ca_crt"`
	AgentCert string `json:"agent_crt"`
	AgentKey  string `json:"agent_key"`
	PanelAddr string `json:"panel_addr"` // AGENT_PANEL_ADDR
}

// IssueAgentCert issues (or reissues) a node agent certificate. CN = node name.
func (s *Service) IssueAgentCert(ctx context.Context, id int64) (AgentCert, string, error) {
	n, err := s.Store.GetNode(ctx, id)
	if err != nil {
		return AgentCert{}, "", err
	}
	if s.PKIDir == "" {
		return AgentCert{}, "", invalid("PKI is not configured (PANEL_PKI_DIR)")
	}
	identity := n.Name
	if n.ActiveInstallation != 0 {
		ins, e := s.Store.Installations(ctx, id)
		if e != nil {
			return AgentCert{}, "", e
		}
		for _, i := range ins {
			if i.ID == n.ActiveInstallation {
				identity = i.Identity
			}
		}
	}
	return s.issueIdentity(identity)
}

func (s *Service) issueIdentity(identity string) (AgentCert, string, error) {
	if err := pki.IssueAgent(s.PKIDir, identity); err != nil {
		return AgentCert{}, "", err
	}
	p := pki.Paths{Dir: s.PKIDir}
	read := func(path string) (string, error) {
		b, err := os.ReadFile(path)
		return string(b), err
	}
	var c AgentCert
	var err error
	if c.CACert, err = read(p.CACert()); err != nil {
		return AgentCert{}, "", err
	}
	if c.AgentCert, err = read(p.AgentCert(identity)); err != nil {
		return AgentCert{}, "", err
	}
	if c.AgentKey, err = read(p.AgentKey(identity)); err != nil {
		return AgentCert{}, "", err
	}
	c.PanelAddr = s.AgentAddr
	s.log().Info("agent certificate issued", "node", identity)
	return c, identity, nil
}

// DefaultAptRepo is the project apt repository on GitHub Pages (.github/workflows/release.yml).
const DefaultAptRepo = "https://lavr-art.github.io/giocondo"

// AptRepoURL is the apt repository address for the agent install instructions.
func (s *Service) AptRepoURL() string {
	if s.AptRepo != "" {
		return strings.TrimSuffix(s.AptRepo, "/")
	}
	return DefaultAptRepo
}

// agentInstall returns everything needed to install a node agent: certificates (new each time), install-agent.sh
// and README, plus the install command run from the directory with these files. The archive
// (AgentBundle), SSH installation and cloud-init are built from it.
//
// firewall adds `--firewall` with the protocol ports (ufw) to the command; sshPort > 0 and not 22
// also adds `--ssh-port`.
func (s *Service) agentInstall(ctx context.Context, id int64, firewall bool, sshPort int) (files []provision.File, name, cmd string, err error) {
	return s.agentInstallIdentity(ctx, id, firewall, sshPort, "")
}
func (s *Service) agentInstallIdentity(ctx context.Context, id int64, firewall bool, sshPort int, identity string) (files []provision.File, name, cmd string, err error) {
	var c AgentCert
	if identity == "" {
		c, name, err = s.IssueAgentCert(ctx, id)
	} else {
		c, name, err = s.issueIdentity(identity)
	}
	if err != nil {
		return nil, "", "", err
	}
	ins, err := s.Store.ListInbounds(ctx, id)
	if err != nil {
		return nil, "", "", err
	}
	panel := c.PanelAddr
	if panel == "" {
		panel = "<panel-host>:9443"
	}
	fw := firewallSpec(ins)
	cmd = "./install-agent.sh --panel " + shellQuote(panel) + " --repo " + shellQuote(s.AptRepoURL())
	if firewall && fw != "" {
		cmd += " --firewall " + shellQuote(fw)
		if sshPort > 0 && sshPort != 22 {
			cmd += fmt.Sprintf(" --ssh-port %d", sshPort)
		}
	}
	readme := fmt.Sprintf(`Installing the agent for node %[1]s (Debian/Ubuntu, amd64 or arm64)

1. Copy the archive to the node and unpack it:
     scp %[1]s-agent.tar.gz root@<node>:
     ssh root@<node> 'tar xzf %[1]s-agent.tar.gz'
2. On the node as root:
     cd %[1]s && ./install-agent.sh --panel %[2]s --repo %[3]s
   The script adds the apt repository, installs vpnctl-agent and vpnctl-sing-box,
   puts the certificates into /etc/vpnctl/agent and the panel address into /etc/vpnctl/agent.env.
   Without repository access: --deb vpnctl-agent_*.deb --deb vpnctl-sing-box_*.deb.
   Close everything except SSH and the node protocol ports (ufw): --firewall '%[4]s'.
3. Logs: journalctl -u vpnctl-agent -f. The node becomes online in the panel.

agent.key is a secret: do not send it in plain text.
`, name, panel, s.AptRepoURL(), fw)
	files = []provision.File{
		{Path: "ca.crt", Mode: 0o644, Data: []byte(c.CACert)},
		{Path: "agent.crt", Mode: 0o644, Data: []byte(c.AgentCert)},
		{Path: "agent.key", Mode: 0o600, Data: []byte(c.AgentKey)},
		{Path: "README.txt", Mode: 0o644, Data: []byte(readme)},
	}
	err = fs.WalkDir(apt.Files, ".", func(path string, d fs.DirEntry, err error) error {
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
		files = append(files, provision.File{Path: path, Mode: mode, Data: data})
		return nil
	})
	if err != nil {
		return nil, "", "", err
	}
	return files, name, cmd, nil
}

// firewallSpec lists the node protocol ports for `install-agent.sh --firewall`: VLESS over TCP,
// Hysteria2 and WireGuard over UDP; Hysteria2 with Let's Encrypt also needs 80/tcp (HTTP-01).
func firewallSpec(ins []store.Inbound) string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, in := range ins {
		switch in.Spec.Type {
		case singbox.TypeVLESSReality:
			add(fmt.Sprintf("%d/tcp", in.Spec.Port))
		case singbox.TypeHysteria2:
			add(fmt.Sprintf("%d/udp", in.Spec.Port))
			if in.Spec.Hysteria2 != nil && in.Spec.Hysteria2.TLSMode() == singbox.TLSACME {
				add("80/tcp")
			}
		case singbox.TypeWireGuard:
			add(fmt.Sprintf("%d/udp", in.Spec.Port))
		}
	}
	return strings.Join(out, ",")
}

// shellQuote quotes an sh argument in single quotes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// AgentBundle is a tar.gz for installing the agent: certificates, install-agent.sh and a README with the command.
// The agent and sing-box themselves are installed as deb packages from the apt repository (AptRepoURL).
func (s *Service) AgentBundle(ctx context.Context, id int64) ([]byte, string, error) {
	files, name, _, err := s.agentInstall(ctx, id, false, 0)
	if err != nil {
		return nil, "", err
	}
	data, err := provision.TarGz(name, files, s.now())
	if err != nil {
		return nil, "", err
	}
	return data, name + "-agent.tar.gz", nil
}
