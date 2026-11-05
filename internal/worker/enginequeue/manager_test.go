package enginequeue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/repository/enginefs"
	"github.com/artemlavr/giocondo/internal/service/engine"
)

func TestWorkersIsolateAndSupersede(t *testing.T) {
	a, da := fixture(t)
	b, db := fixture(t)
	db.id = "second"
	entered := make(chan struct{})
	release := make(chan struct{})
	da.collect = func(ctx context.Context) (map[string]engine.Traffic, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}
	m, e := NewManager(a, b)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NewManager(a, a); e == nil {
		t.Fatal("duplicate ID accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	defer func() { cancel(); m.Wait() }()
	one := m.Submit("sing-box", []byte("one"), engine.Hash([]byte("one")))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	two := m.Submit("sing-box", []byte("two"), engine.Hash([]byte("two")))
	three := m.Submit("sing-box", []byte("three"), engine.Hash([]byte("three")))
	select {
	case <-two.Done():
		e := two.Err()
		if e == nil {
			t.Fatal("superseded reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("superseded not completed")
	}
	other := m.Submit("second", []byte("other"), engine.Hash([]byte("other")))
	select {
	case <-other.Done():
		e := other.Err()
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("second engine blocked")
	}
	close(release)
	if e := one.Wait(ctx); e != nil {
		t.Fatal(e)
	}
	if e := three.Wait(ctx); e != nil {
		t.Fatal(e)
	}
	cancel()
}

func TestLatestCurrentRequestSupersedesPending(t *testing.T) {
	r, d := fixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	d.collect = func(ctx context.Context) (map[string]engine.Traffic, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}
	m, e := NewManager(r)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); m.Wait() }()
	m.Start(ctx)
	a := m.Submit(d.ID(), []byte("A"), engine.Hash([]byte("A")))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker not started")
	}
	b := m.Submit(d.ID(), []byte("B"), engine.Hash([]byte("B")))
	again := m.Submit(d.ID(), []byte("A"), engine.Hash([]byte("A")))
	if again != a {
		t.Fatal("duplicate did not share future")
	}
	select {
	case <-b.Done():
		if b.Err() == nil {
			t.Fatal("pending B succeeded")
		}
	case <-time.After(100 * time.Millisecond):
		close(release)
		t.Fatal("pending obsolete B was retained")
	}
	close(release)
	if e = a.Wait(ctx); e != nil {
		t.Fatal(e)
	}
}

type testDriver struct {
	mu              sync.Mutex
	id              string
	config          string
	restarts, stops int
	active          bool
	failRestart     bool
	collect         func(context.Context) (map[string]engine.Traffic, error)
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

func (d *testDriver) Collect(ctx context.Context) (map[string]engine.Traffic, error) {
	if d.collect != nil {
		return d.collect(ctx)
	}
	return nil, nil
}

func fixture(t *testing.T) (*engine.Runtime, *testDriver) {
	t.Helper()
	dir := t.TempDir()
	d := &testDriver{id: "sing-box", config: filepath.Join(dir, "etc", "config.json")}
	r := engine.New(d, enginefs.NewStore(), d.config, filepath.Join(dir, "state"), 0, nil)
	if e := r.Open(context.Background()); e != nil {
		t.Fatal(e)
	}
	return r, d
}
