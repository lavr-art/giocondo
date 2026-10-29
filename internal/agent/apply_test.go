package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnv provides shell stubs for sing-box and systemctl:
//   - sing-box check fails if the config contains "BROKEN";
//   - restart appends a line to restarts.log;
//   - status succeeds while the config has no "CRASH" (sing-box "does not start" with such a config).
type fakeEnv struct {
	dir     string
	applier *Applier
}

func newFakeEnv(t *testing.T) *fakeEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "etc", "config.json")
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	singbox := write("sing-box", `
case "$1" in
  version) echo "sing-box version 1.14.4"; echo "Environment: test" ;;
  check) if grep -q BROKEN "$3"; then echo "decode config: BROKEN" >&2; exit 1; fi ;;
esac
`)
	restart := write("restart", `echo restart >> "`+filepath.Join(dir, "restarts.log")+`"`+"\n")
	status := write("status", `! grep -q CRASH "`+cfg+`"`+"\n")
	return &fakeEnv{dir: dir, applier: &Applier{
		SingBoxBin: singbox, ConfigPath: cfg,
		RestartCmd: []string{restart}, StatusCmd: []string{status},
	}}
}

func (e *fakeEnv) restarts(t *testing.T) int {
	data, err := os.ReadFile(filepath.Join(e.dir, "restarts.log"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "restart")
}

func (e *fakeEnv) config(t *testing.T) string {
	data, err := os.ReadFile(e.applier.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestApplySuccess(t *testing.T) {
	ctx := context.Background()
	e := newFakeEnv(t)
	if e.applier.CurrentHash() != "" {
		t.Fatal("hash without config")
	}
	if err := e.applier.Apply(ctx, []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	if e.config(t) != `{"v":1}` || e.restarts(t) != 1 {
		t.Errorf("config %q, restarts %d", e.config(t), e.restarts(t))
	}
	if e.applier.CurrentHash() != Hash([]byte(`{"v":1}`)) {
		t.Error("hash mismatch")
	}
	if st, _ := os.Stat(e.applier.ConfigPath); st.Mode().Perm() != 0o600 {
		t.Errorf("config mode %v", st.Mode().Perm())
	}
	if err := e.applier.Apply(ctx, []byte(`{"v":2}`)); err != nil {
		t.Fatal(err)
	}
	prev, _ := os.ReadFile(e.applier.ConfigPath + ".prev")
	if string(prev) != `{"v":1}` || e.config(t) != `{"v":2}` {
		t.Errorf("prev %q, config %q", prev, e.config(t))
	}
	if v := e.applier.Version(ctx); v != "sing-box version 1.14.4" {
		t.Errorf("version %q", v)
	}
	// No temporary files are left.
	entries, _ := os.ReadDir(filepath.Dir(e.applier.ConfigPath))
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), ".") {
			t.Errorf("leftover temp file %s", en.Name())
		}
	}
}

func TestApplyCheckFails(t *testing.T) {
	ctx := context.Background()
	e := newFakeEnv(t)
	if err := e.applier.Apply(ctx, []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	err := e.applier.Apply(ctx, []byte(`{"v":"BROKEN"}`))
	if err == nil || !strings.Contains(err.Error(), "sing-box check") {
		t.Fatalf("err = %v", err)
	}
	if e.config(t) != `{"v":1}` || e.restarts(t) != 1 {
		t.Errorf("config touched: %q, restarts %d", e.config(t), e.restarts(t))
	}
}

func TestApplyRollback(t *testing.T) {
	ctx := context.Background()
	e := newFakeEnv(t)
	collected := 0
	e.applier.BeforeRestart = func(context.Context) { collected++ }
	if err := e.applier.Apply(ctx, []byte(`{"v":1}`)); err != nil {
		t.Fatal(err)
	}
	err := e.applier.Apply(ctx, []byte(`{"v":"CRASH"}`))
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("err = %v", err)
	}
	// A restart with the new config and another one with the old.
	if e.config(t) != `{"v":1}` || e.restarts(t) != 3 || collected != 3 {
		t.Errorf("config %q, restarts %d, collected %d", e.config(t), e.restarts(t), collected)
	}
	if !e.applier.Active(ctx) {
		t.Error("service is not active after rollback")
	}
}

func TestApplyFirstConfigCrashes(t *testing.T) {
	e := newFakeEnv(t)
	err := e.applier.Apply(context.Background(), []byte(`{"v":"CRASH"}`))
	if err == nil || !strings.Contains(err.Error(), "no previous") {
		t.Fatalf("err = %v", err)
	}
}
