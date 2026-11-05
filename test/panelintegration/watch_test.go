package panelintegration

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	. "github.com/artemlavr/giocondo/test/paneltest" //nolint:revive // dot import keeps the test package call sites concise.
)

type events struct {
	mu   sync.Mutex
	list []panel.Event
}

func (e *events) add(ev panel.Event) { e.mu.Lock(); e.list = append(e.list, ev); e.mu.Unlock() }

// take returns the collected "type:name" events and clears the list.
func (e *events) take() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, ev := range e.list {
		name := ""
		switch {
		case ev.User != nil:
			name = ev.User.Name
		case ev.Node != nil:
			name = ev.Node.Name
		}
		out = append(out, ev.Type+":"+name)
	}
	e.list = nil
	return out
}

func expectEvents(t *testing.T, ev *events, step string, want ...string) {
	t.Helper()

	got := ev.take()
	if !slices.Equal(got, want) {
		t.Fatalf("%s: events %v, want %v", step, got, want)
	}
}

func TestWatchEvents(t *testing.T) {
	ctx := context.Background()
	clk := newClock(time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC))
	svc, kicks := newService(t, clk)
	ev := &events{}
	svc.configure(t, func(_ *Config, deps *Dependencies) { deps.Notify = ev.add })
	st := svc.Store

	alice, _ := svc.Users.CreateUser(ctx, panel.CreateUserInput{Name: "alice", TrafficLimitBytes: 100, Days: 30})
	bob, _ := svc.Users.CreateUser(ctx, panel.CreateUserInput{Name: "bob", Days: 2})
	expectEvents(t, ev, "create", "user_created:alice", "user_created:bob")
	node := panel.Node{Name: "n1", Host: "n1.example.com"}
	if err := st.Nodes.CreateNode(ctx, &node); err != nil {
		t.Fatal(err)
	}
	traffic := func(n int64) {
		t.Helper()

		if err := st.Traffic.AddTraffic(ctx, []panel.TrafficDelta{{UserID: alice.ID, NodeID: node.ID, At: clk.now(), Down: n}}); err != nil {
			t.Fatal(err)
		}
	}
	watch := func() {
		t.Helper()

		if err := svc.Watcher.WatchOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}

	// First check: statuses are remembered silently, bob's term ends in 2 days.
	watch()
	expectEvents(t, ev, "first watch", "user_expiring:bob")
	watch()
	expectEvents(t, ev, "nothing changed")

	traffic(85)
	watch()
	expectEvents(t, ev, "85%", "user_traffic_warning:alice")
	watch()
	expectEvents(t, ev, "warning is sent once")

	base := kicks.Load()
	traffic(20)
	watch()
	expectEvents(t, ev, "limit", "user_limited:alice")
	if kicks.Load() == base {
		t.Error("limit did not kick nodes")
	}

	// A traffic reset clears the flag: the next 80% warns again.
	if _, err := svc.Users.ResetTraffic(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	expectEvents(t, ev, "reset", "user_updated:alice")
	watch()
	expectEvents(t, ev, "after reset")
	traffic(90)
	watch()
	expectEvents(t, ev, "80% again", "user_traffic_warning:alice")

	clk.set(clk.now().Add(3 * 24 * time.Hour))
	watch()
	expectEvents(t, ev, "bob expired", "user_expired:bob")

	// An extension clears the term flag: the warning comes again 3 days before the new term.
	if _, err := svc.Users.Extend(ctx, bob.ID, 5); err != nil {
		t.Fatal(err)
	}
	expectEvents(t, ev, "extend", "user_updated:bob")
	watch()
	expectEvents(t, ev, "extended")
	clk.set(clk.now().Add(3 * 24 * time.Hour))
	watch()
	expectEvents(t, ev, "bob expiring again", "user_expiring:bob")

	// Auto-deletion: bob's term expired more than a day ago.
	svc.configure(t, func(cfg *Config, _ *Dependencies) { cfg.AutoDeleteExpiredDays = 1 })
	clk.set(clk.now().Add(4 * 24 * time.Hour))
	watch()
	expectEvents(t, ev, "autodelete", "user_deleted:bob")
	if _, err := st.Users.GetUser(ctx, bob.ID); err == nil {
		t.Error("bob was not deleted")
	}
}

func TestWatchNodes(t *testing.T) {
	ctx := context.Background()
	clk := newClock(time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC))
	svc, _ := newService(t, clk)
	ev := &events{}
	svc.configure(t, func(_ *Config, deps *Dependencies) { deps.Notify = ev.add })
	st := svc.Store
	n := panel.Node{Name: "n1", Host: "n1.example.com"}
	off := panel.Node{Name: "off", Host: "off.example.com", Status: panel.NodeDisabled}
	for _, x := range []*panel.Node{&n, &off} {
		if err := st.Nodes.CreateNode(ctx, x); err != nil {
			t.Fatal(err)
		}
	}
	watch := func() {
		t.Helper()

		if err := svc.Watcher.WatchOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	watch()
	expectEvents(t, ev, "new node")

	_ = st.Nodes.TouchNode(ctx, n.ID, panel.NodeOffline, clk.now())
	clk.set(clk.now().Add(time.Minute))
	watch()
	expectEvents(t, ev, "short disconnect")

	clk.set(clk.now().Add(2 * time.Minute))
	watch()
	expectEvents(t, ev, "agent disconnect alone is not a protocol failure")
	watch()
	expectEvents(t, ev, "down once")

	_ = st.Nodes.TouchNode(ctx, n.ID, panel.NodeOnline, clk.now())
	watch()
	expectEvents(t, ev, "agent reconnect alone is not a protocol recovery")
	watch()
	expectEvents(t, ev, "up once")
}
