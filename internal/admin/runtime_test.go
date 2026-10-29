package admin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/artemlavr/giocondo/internal/store"
)

func TestSetEnvLine(t *testing.T) {
	in := "# AGENT_PANEL_ADDR=commented\nAGENT_PANEL_ADDR=old:1\nAGENT_LOG_LEVEL=debug\n"
	want := "# AGENT_PANEL_ADDR=commented\nAGENT_PANEL_ADDR=127.0.0.1:9443\nAGENT_LOG_LEVEL=debug\n"
	if got := string(setEnvLine([]byte(in), "AGENT_PANEL_ADDR", "127.0.0.1:9443")); got != want {
		t.Errorf("got %q", got)
	}
	if got := string(setEnvLine(nil, "K", "v")); got != "K=v\n" {
		t.Errorf("empty env: %q", got)
	}
}

func TestLocalRuntimeInstall(t *testing.T) {
	dir := t.TempDir()
	example := filepath.Join(dir, "agent.env.example")
	if err := os.WriteFile(example, []byte("AGENT_PANEL_ADDR=\n# AGENT_LOG_LEVEL=info\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "restarted")
	l := &LocalRuntime{EtcDir: filepath.Join(dir, "etc"), EnvExample: example, RestartCmd: []string{"touch", marker}}
	cert := AgentCert{CACert: "CA", AgentCert: "CRT", AgentKey: "KEY", PanelAddr: "127.0.0.1:9443"}
	if err := l.Install(context.Background(), store.Node{Name: "local"}, cert); err != nil {
		t.Fatal(err)
	}
	read := func(p string) (string, os.FileMode) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(l.EtcDir, p))
		if err != nil {
			t.Fatal(err)
		}
		st, _ := os.Stat(filepath.Join(l.EtcDir, p))
		return string(data), st.Mode().Perm()
	}
	if key, mode := read("agent/agent.key"); key != "KEY" || mode != 0o600 {
		t.Errorf("agent.key %q %o", key, mode)
	}
	if env, mode := read("agent.env"); env != "AGENT_PANEL_ADDR=127.0.0.1:9443\n# AGENT_LOG_LEVEL=info\n" || mode != 0o600 {
		t.Errorf("agent.env %q %o", env, mode)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("agent was not restarted")
	}
	// A repeated installation uses the existing env, not the template.
	if err := os.WriteFile(filepath.Join(l.EtcDir, "agent.env"), []byte("AGENT_PANEL_ADDR=x\nAGENT_LOG_LEVEL=debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.Install(context.Background(), store.Node{Name: "local"}, cert); err != nil {
		t.Fatal(err)
	}
	if env, _ := read("agent.env"); env != "AGENT_PANEL_ADDR=127.0.0.1:9443\nAGENT_LOG_LEVEL=debug\n" {
		t.Errorf("reinstall env %q", env)
	}
}
