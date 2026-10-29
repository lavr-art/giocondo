package admin

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/store"
)

// clock is a test clock that is safe to read from the Watch goroutine.
type clock struct{ unix atomic.Int64 }

func newClock(t time.Time) *clock { c := &clock{}; c.set(t); return c }
func (c *clock) set(t time.Time)  { c.unix.Store(t.Unix()) }
func (c *clock) now() time.Time   { return time.Unix(c.unix.Load(), 0).UTC() }

func newService(t *testing.T, clk *clock) (*Service, *atomic.Int32) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	kicks := &atomic.Int32{}
	return &Service{Store: st, Kick: func() { kicks.Add(1) }, Now: clk.now}, kicks
}

// Watch calls Kick when a user becomes inactive by term,
// and does not call it while nothing changes.
func TestWatchKicksOnExpiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clk := newClock(time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC))
	svc, kicks := newService(t, clk)
	if _, err := svc.CreateUser(ctx, CreateUserInput{Name: "alice", Days: 1}); err != nil {
		t.Fatal(err)
	}
	base := kicks.Load()
	go svc.Watch(ctx, 20*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	if kicks.Load() != base {
		t.Fatalf("kicked without changes: %d", kicks.Load()-base)
	}
	clk.set(clk.now().AddDate(0, 0, 2)) // the term has expired
	deadline := time.Now().Add(2 * time.Second)
	for kicks.Load() == base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if kicks.Load() == base {
		t.Fatal("expiry did not kick nodes")
	}
}

func TestStatusOrder(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	svc, _ := newService(t, newClock(now))
	past := now.Add(-time.Hour)
	cases := []struct {
		u    store.User
		used int64
		want string
	}{
		{store.User{Enabled: true}, 0, StatusActive},
		{store.User{Enabled: false, ExpiresAt: &past, TrafficLimitBytes: 1}, 5, StatusDisabled}, // disabled takes precedence over everything
		{store.User{Enabled: true, ExpiresAt: &past, TrafficLimitBytes: 1}, 5, StatusExpired},
		{store.User{Enabled: true, TrafficLimitBytes: 10}, 10, StatusLimit},
		{store.User{Enabled: true, TrafficLimitBytes: 10}, 9, StatusActive},
	}
	for i, c := range cases {
		if got := svc.view(c.u, store.Usage{Down: c.used}).Status; got != c.want {
			t.Errorf("case %d: %s, want %s", i, got, c.want)
		}
	}
}
