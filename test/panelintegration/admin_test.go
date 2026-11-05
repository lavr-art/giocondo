package panelintegration

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
	"github.com/artemlavr/giocondo/internal/worker/watchsched"
	. "github.com/artemlavr/giocondo/test/paneltest" //nolint:revive // dot import keeps the test package call sites concise.
)

// clock is a test clock that is safe to read from the Watch goroutine.
type clock struct{ unix atomic.Int64 }

func newClock(t time.Time) *clock { c := &clock{}; c.set(t); return c }
func (c *clock) set(t time.Time)  { c.unix.Store(t.Unix()) }
func (c *clock) now() time.Time   { return time.Unix(c.unix.Load(), 0).UTC() }

func newService(t *testing.T, clk *clock) (*serviceFixture, *atomic.Int32) {
	t.Helper()

	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	kicks := &atomic.Int32{}
	f := &serviceFixture{Store: NewStore(st), cfg: Config{}, deps: Dependencies{DB: st, Kick: func() { kicks.Add(1) }, Now: clk.now}}
	f.configure(t, func(*Config, *Dependencies) {})
	return f, kicks
}

// Watch calls Kick when a user becomes inactive by term,
// and does not call it while nothing changes.
func TestWatchKicksOnExpiry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clk := newClock(time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC))
	svc, kicks := newService(t, clk)
	if _, err := svc.Users.CreateUser(ctx, panel.CreateUserInput{Name: "alice", Days: 1}); err != nil {
		t.Fatal(err)
	}
	base := kicks.Load()
	go (&watchsched.Worker{Service: svc.Watcher, Interval: 20 * time.Millisecond}).Run(ctx)
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
		u    panel.User
		used int64
		want string
	}{
		{panel.User{Enabled: true}, 0, string(panel.UserActive)},
		{panel.User{Enabled: false, ExpiresAt: &past, TrafficLimitBytes: 1}, 5, string(panel.UserDisabled)}, // disabled takes precedence over everything
		{panel.User{Enabled: true, ExpiresAt: &past, TrafficLimitBytes: 1}, 5, string(panel.UserExpired)},
		{panel.User{Enabled: true, TrafficLimitBytes: 10}, 10, string(panel.UserLimited)},
		{panel.User{Enabled: true, TrafficLimitBytes: 10}, 9, string(panel.UserActive)},
	}
	for i, c := range cases {
		if got := svc.Users.View(c.u, panel.Usage{Down: c.used}).Status; got != c.want {
			t.Errorf("case %d: %s, want %s", i, got, c.want)
		}
	}
}

// A fixture keeps database setup separate from the application facade.
type serviceFixture struct {
	*Components
	Store *Store
	cfg   Config
	deps  Dependencies
}

func (f *serviceFixture) configure(t *testing.T, fn func(*Config, *Dependencies)) {
	t.Helper()

	fn(&f.cfg, &f.deps)
	svc := New(f.cfg, f.deps)
	t.Cleanup(func() { _ = svc.Close() })
	f.Components = svc
}

func (f *serviceFixture) now() time.Time { return f.deps.Now() }
