package admin

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/store"
)

// Runtime installs a node agent where it will run. LocalRuntime installs it on the panel machine
// (store.RuntimeLocal); remote nodes (store.RuntimeRemote) currently get an archive with
// install-agent.sh (AgentBundle) or an SSH installation.
type Runtime interface {
	Install(ctx context.Context, n store.Node, cert AgentCert) error
}

// LocalRuntime is the agent from the vpnctl-agent package on this machine. It runs as root:
// writes the agent certificates and env and restarts it. The layout matches the deb (deploy/apt/deb).
type LocalRuntime struct {
	EtcDir     string   // "/etc/vpnctl"
	EnvExample string   // env template from the package: "/usr/share/vpnctl/agent.env.example"
	RestartCmd []string // nil means systemctl enable + restart; an empty slice means no restart
}

// DefaultLocalRuntime is the deb package layout.
func DefaultLocalRuntime() *LocalRuntime {
	return &LocalRuntime{EtcDir: "/etc/vpnctl", EnvExample: "/usr/share/vpnctl/agent.env.example"}
}

// Install puts the certificates into EtcDir/agent, sets AGENT_PANEL_ADDR in EtcDir/agent.env
// (other lines are left alone) and restarts the agent.
func (l *LocalRuntime) Install(ctx context.Context, n store.Node, cert AgentCert) error {
	if cert.PanelAddr == "" {
		return errors.New("panel address for the local agent is empty")
	}
	dir := filepath.Join(l.EtcDir, "agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, f := range []struct {
		name string
		data string
		mode os.FileMode
	}{
		{"ca.crt", cert.CACert, 0o644},
		{"agent.crt", cert.AgentCert, 0o644},
		{"agent.key", cert.AgentKey, 0o600},
	} {
		if err := writeFileAtomic(filepath.Join(dir, f.name), []byte(f.data), f.mode); err != nil {
			return err
		}
	}
	envPath := filepath.Join(l.EtcDir, "agent.env")
	env, err := os.ReadFile(envPath)
	if errors.Is(err, os.ErrNotExist) {
		env, err = os.ReadFile(l.EnvExample)
		if errors.Is(err, os.ErrNotExist) {
			env, err = nil, nil
		}
	}
	if err != nil {
		return err
	}
	if err := writeFileAtomic(envPath, setEnvLine(env, "AGENT_PANEL_ADDR", cert.PanelAddr), 0o600); err != nil {
		return err
	}
	return l.restart(ctx)
}

func (l *LocalRuntime) restart(ctx context.Context) error {
	cmds := [][]string{
		{"systemctl", "enable", "vpnctl-sing-box.service", "vpnctl-agent.service"},
		{"systemctl", "restart", "vpnctl-agent.service"},
	}
	if l.RestartCmd != nil {
		cmds = nil
		if len(l.RestartCmd) > 0 {
			cmds = [][]string{l.RestartCmd}
		}
	}
	for _, c := range cmds {
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		out, err := exec.CommandContext(cctx, c[0], c[1:]...).CombinedOutput()
		cancel()
		if err != nil {
			return fmt.Errorf("%s: %w: %s", strings.Join(c, " "), err, bytes.TrimSpace(out))
		}
	}
	return nil
}

// setEnvLine replaces the KEY=... line (the first uncommented one) or appends it at the end.
func setEnvLine(env []byte, key, value string) []byte {
	var out bytes.Buffer
	found := false
	sc := bufio.NewScanner(bytes.NewReader(env))
	for sc.Scan() {
		line := sc.Text()
		if !found && strings.HasPrefix(line, key+"=") {
			line, found = key+"="+value, true
		}
		out.WriteString(line + "\n")
	}
	if !found {
		out.WriteString(key + "=" + value + "\n")
	}
	return out.Bytes()
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil { // WriteFile does not change the mode of an existing file
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
