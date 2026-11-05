package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type testDriver struct {
	mu              sync.Mutex
	id              string
	config          string
	restarts, stops int
	active          bool
	failRestart     bool
	collect         func(context.Context) (map[string]Traffic, error)
}

func (d *testDriver) ID() string                              { return d.id }
func (d *testDriver) Version(context.Context) (string, error) { return "test-1", nil }
func (d *testDriver) Validate(_ context.Context, p string) error {
	b, e := os.ReadFile(p)
	if e != nil {
		return e
	}
	if strings.Contains(string(b), "BROKEN") {
		return errors.New("invalid config")
	}
	return nil
}
func (d *testDriver) Restart(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.restarts++
	b, _ := os.ReadFile(d.config)
	d.active = !strings.Contains(string(b), "CRASH")
	if d.failRestart {
		return errors.New("restart failed")
	}
	return nil
}
func (d *testDriver) Stop(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stops++
	d.active = false
	return nil
}
func (d *testDriver) Active(context.Context) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active, nil
}
func (d *testDriver) Collect(ctx context.Context) (map[string]Traffic, error) {
	if d.collect != nil {
		return d.collect(ctx)
	}
	return nil, nil
}
func fixture(t *testing.T) (*Runtime, *testDriver) {
	t.Helper()
	dir := t.TempDir()
	d := &testDriver{id: "sing-box", config: filepath.Join(dir, "etc", "config.json")}
	r := &Runtime{Driver: d, ConfigPath: d.config, StateDir: filepath.Join(dir, "state")}
	if e := r.Open(context.Background()); e != nil {
		t.Fatal(e)
	}
	return r, d
}
func TestApplyAndRollback(t *testing.T) {
	r, d := fixture(t)
	ctx := context.Background()
	old := []byte(`{"v":1}`)
	if e := r.Apply(ctx, old, Hash(old)); e != nil {
		t.Fatal(e)
	}
	if e := r.Apply(ctx, old, Hash(old)); e != nil {
		t.Fatal(e)
	}
	if d.restarts != 1 {
		t.Fatalf("unchanged restart: %d", d.restarts)
	}
	for _, bad := range []string{"BROKEN", "CRASH"} {
		if e := r.Apply(ctx, []byte(bad), Hash([]byte(bad))); e == nil {
			t.Fatal("accepted", bad)
		}
		b, _ := os.ReadFile(d.config)
		if string(b) != string(old) || r.Snapshot().AppliedHash != Hash(old) {
			t.Fatal("lost working config")
		}
	}
	if d.restarts != 3 {
		t.Fatalf("restarts %d", d.restarts)
	}
	if r.Snapshot().RejectedHash != Hash([]byte("CRASH")) {
		t.Fatal("lost rejection")
	}
}
func TestFirstConfigFailureStopsAndRemoves(t *testing.T) {
	r, d := fixture(t)
	if e := r.Apply(context.Background(), []byte("CRASH"), Hash([]byte("CRASH"))); e == nil {
		t.Fatal("accepted")
	}
	if _, e := os.Stat(d.config); !errors.Is(e, os.ErrNotExist) {
		t.Fatalf("config left: %v", e)
	}
	if d.stops != 1 || r.Snapshot().AppliedHash != "" {
		t.Fatal("failed config active")
	}
}
func TestAdoptsExistingWithoutRestart(t *testing.T) {
	r, d := fixture(t)
	if e := os.MkdirAll(filepath.Dir(d.config), 0700); e != nil {
		t.Fatal(e)
	}
	b := []byte("imported WG keys")
	if e := os.WriteFile(d.config, b, 0600); e != nil {
		t.Fatal(e)
	}
	r2 := &Runtime{Driver: d, ConfigPath: d.config, StateDir: r.StateDir}
	if e := r2.Open(context.Background()); e != nil {
		t.Fatal(e)
	}
	if d.restarts != 0 || r2.Snapshot().AppliedHash != Hash(b) {
		t.Fatal("bootstrap changed existing service")
	}
}
func TestHashMismatchDoesNotTouchService(t *testing.T) {
	r, d := fixture(t)
	if e := r.Apply(context.Background(), []byte("bad"), Hash([]byte("other"))); e == nil {
		t.Fatal("accepted mismatch")
	}
	if d.restarts != 0 {
		t.Fatal("restarted")
	}
}
func TestRecoveryFailureBlocksOnlyThisRuntime(t *testing.T) {
	r, d := fixture(t)
	ctx := context.Background()
	b := []byte("old")
	if e := r.Apply(ctx, b, Hash(b)); e != nil {
		t.Fatal(e)
	}
	d.failRestart = true
	if e := r.Apply(ctx, []byte("new"), Hash([]byte("new"))); e == nil {
		t.Fatal("accepted")
	}
	if r.Snapshot().Phase != "blocked" {
		t.Fatal(r.Snapshot())
	}
	if e := r.Apply(ctx, b, Hash(b)); e == nil {
		t.Fatal("blocked runtime accepted apply")
	}
	if _, e := os.Stat(filepath.Join(r.StateDir, "journal.json")); e != nil {
		t.Fatal(e)
	}
}

type statusDriver struct {
	*testDriver
	statusError error
}

func (d *statusDriver) Active(context.Context) (bool, error) { return d.active, d.statusError }
func TestStatusErrorClearsAfterSuccessfulCheck(t *testing.T) {
	r, d := fixture(t)
	driver := &statusDriver{testDriver: d, statusError: errors.New("status unavailable")}
	r.Driver = driver
	r.Refresh(context.Background())
	if r.Snapshot().Error != "engine status check failed" {
		t.Fatal(r.Snapshot())
	}
	driver.statusError = nil
	r.Refresh(context.Background())
	if r.Snapshot().Error != "" {
		t.Fatal("stale health error", r.Snapshot())
	}
}
