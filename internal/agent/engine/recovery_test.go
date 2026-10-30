package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCrashRecovery(t *testing.T) {
	if dir := os.Getenv("ENGINE_CRASH_DIR"); dir != "" {
		d := &testDriver{id: "sing-box", config: filepath.Join(dir, "config.json")}
		r := &Runtime{Driver: d, ConfigPath: d.config, StateDir: filepath.Join(dir, "state")}
		if e := r.Open(context.Background()); e != nil {
			t.Fatal(e)
		}
		r.checkpoint = func(phase string) {
			if phase == os.Getenv("ENGINE_CRASH_PHASE") {
				os.Exit(73)
			}
		}
		payload := []byte("new")
		if os.Getenv("ENGINE_CRASH_PHASE") == "restoring" {
			payload = []byte("CRASH")
		}
		_ = r.Apply(context.Background(), payload, Hash(payload))
		t.Fatal("did not reach checkpoint")
	}
	for _, first := range []bool{false, true} {
		for _, phase := range []string{"prepared", "activating", "replaced", "started", "restoring", "committed"} {
			t.Run(phase+map[bool]string{true: "-first", false: "-existing"}[first], func(t *testing.T) {
				dir := t.TempDir()
				config := filepath.Join(dir, "config.json")
				if !first {
					if e := os.WriteFile(config, []byte("old"), 0600); e != nil {
						t.Fatal(e)
					}
				}
				cmd := exec.Command(os.Args[0], "-test.run=^TestCrashRecovery$")
				cmd.Env = append(os.Environ(), "ENGINE_CRASH_DIR="+dir, "ENGINE_CRASH_PHASE="+phase)
				if e := cmd.Run(); e == nil {
					t.Fatal("child did not crash")
				} else if exit, ok := e.(*exec.ExitError); !ok || exit.ExitCode() != 73 {
					t.Fatalf("child: %v", e)
				}
				d := &testDriver{id: "sing-box", config: config}
				r := &Runtime{Driver: d, ConfigPath: config, StateDir: filepath.Join(dir, "state")}
				if e := r.Open(context.Background()); e != nil {
					t.Fatal(e)
				}
				data, e := os.ReadFile(config)
				if phase == "committed" {
					if e != nil || string(data) != "new" || d.restarts != 0 {
						t.Fatal("lost committed config")
					}
				} else if first {
					if !os.IsNotExist(e) || d.stops != 1 {
						t.Fatalf("failed first config left: %s %v", data, e)
					}
				} else {
					if e != nil || string(data) != "old" || d.restarts != 1 {
						t.Fatal("did not restore old config")
					}
				}
				target := []byte("new")
				if phase == "restoring" {
					target = []byte("CRASH")
				}
				if phase != "committed" && r.Snapshot().RejectedHash != Hash(target) {
					t.Fatal("lost interrupted target")
				}
			})
		}
	}
}
func TestStateLock(t *testing.T) {
	dir := t.TempDir()
	a, e := Lock(dir)
	if e != nil {
		t.Fatal(e)
	}
	if b, e := Lock(dir); e == nil {
		_ = b.Close()
		t.Fatal("second lock acquired")
	}
	_ = a.Close()
	b, e := Lock(dir)
	if e != nil {
		t.Fatal(e)
	}
	_ = b.Close()
}
func TestJournalWriteFailureDoesNotActivate(t *testing.T) {
	r, d := fixture(t)
	if e := os.WriteFile(r.StateDir, []byte("not a directory"), 0600); e != nil {
		t.Fatal(e)
	}
	e := r.Apply(context.Background(), []byte("new"), Hash([]byte("new")))
	if e == nil || d.restarts != 0 {
		t.Fatal("activated without journal")
	}
}

func TestCorruptJournalBlocksWithoutChangingRunningConfig(t *testing.T) {
	r, d := fixture(t)
	ctx := context.Background()
	data := []byte("existing WG credentials")
	if e := r.Apply(ctx, data, Hash(data)); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(r.StateDir, "journal.json"), []byte("broken"), 0600); e != nil {
		t.Fatal(e)
	}
	before := d.restarts
	next := &Runtime{Driver: d, ConfigPath: r.ConfigPath, StateDir: r.StateDir}
	if e := next.Open(ctx); e == nil {
		t.Fatal("corrupt journal accepted")
	}
	state := next.Snapshot()
	if state.Phase != "blocked" || !state.Active {
		t.Fatal("blocked health hid running service", state)
	}
	if e := next.Apply(ctx, []byte("replacement"), Hash([]byte("replacement"))); e == nil {
		t.Fatal("applied while blocked")
	}
	b, e := os.ReadFile(r.ConfigPath)
	if e != nil || string(b) != string(data) || d.restarts != before {
		t.Fatal("corrupt journal changed running config")
	}
}
