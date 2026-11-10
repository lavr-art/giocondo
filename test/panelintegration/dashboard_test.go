package panelintegration

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

func TestDashboardOverview(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	svc, _ := newService(t, newClock(now))
	st := svc.Store

	alice, _ := svc.Users.CreateUser(ctx, panel.CreateUserInput{Name: "alice", TrafficLimitBytes: 500})
	bob, _ := svc.Users.CreateUser(ctx, panel.CreateUserInput{Name: "bob", Days: 2})
	carol, _ := svc.Users.CreateUser(ctx, panel.CreateUserInput{Name: "carol"})
	if _, err := svc.Users.UpdateUser(ctx, carol.ID, panel.UpdateUserInput{Enabled: new(false)}); err != nil {
		t.Fatal(err)
	}

	n1 := panel.Node{Name: "n1", Host: "n1.example.com", Status: panel.NodeOnline}
	n2 := panel.Node{Name: "n2", Host: "n2.example.com", Status: panel.NodeOffline}
	for _, n := range []*panel.Node{&n1, &n2} {
		if err := st.Nodes.CreateNode(ctx, n); err != nil {
			t.Fatal(err)
		}
	}

	day := func(ago int) time.Time { return now.AddDate(0, 0, -ago) }
	deltas := []panel.TrafficDelta{
		{UserID: alice.ID, NodeID: n1.ID, At: day(0), Up: 100, Down: 300},
		{UserID: alice.ID, NodeID: n2.ID, At: day(1), Down: 50},
		{UserID: bob.ID, NodeID: n1.ID, At: day(29), Down: 7},
		{UserID: bob.ID, NodeID: n1.ID, At: day(30), Down: 1000}, // outside the 30-day window
	}
	if err := st.Traffic.AddTraffic(ctx, deltas); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Dashboard.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if want := (panel.DashboardUsers{Total: 3, Active: 2, Disabled: 1}); got.Users != want {
		t.Errorf("users = %+v, want %+v", got.Users, want)
	}
	if want := (panel.DashboardNodes{Total: 2, Online: 1}); got.Nodes != want {
		t.Errorf("nodes = %+v, want %+v", got.Nodes, want)
	}
	if want := (panel.Usage{Up: 100, Down: 300}); got.Today != want {
		t.Errorf("today = %+v, want %+v", got.Today, want)
	}
	if want := (panel.Usage{Up: 100, Down: 357}); got.Period != want {
		t.Errorf("period = %+v, want %+v", got.Period, want)
	}

	if len(got.Days) != panel.DashboardDays {
		t.Fatalf("days = %d, want %d", len(got.Days), panel.DashboardDays)
	}
	checkDay := func(i int, ago int, want panel.Usage) {
		t.Helper()
		d := got.Days[i]
		if !d.Day.Equal(time.Date(2026, 10, 28-ago, 0, 0, 0, 0, time.UTC)) || d.Usage != want {
			t.Errorf("days[%d] = %s %+v, want %d days ago %+v", i, d.Day, d.Usage, ago, want)
		}
	}
	checkDay(0, 29, panel.Usage{Down: 7})
	checkDay(1, 28, panel.Usage{})
	checkDay(28, 1, panel.Usage{Down: 50})
	checkDay(29, 0, panel.Usage{Up: 100, Down: 300})

	wantTop := []panel.NamedUsage{
		{ID: alice.ID, Name: "alice", Usage: panel.Usage{Up: 100, Down: 350}},
		{ID: bob.ID, Name: "bob", Usage: panel.Usage{Down: 7}},
	}
	if !slices.Equal(got.TopUsers, wantTop) {
		t.Errorf("top users = %+v, want %+v", got.TopUsers, wantTop)
	}
	wantNodes := []panel.NamedUsage{
		{ID: n1.ID, Name: "n1", Usage: panel.Usage{Up: 100, Down: 307}},
		{ID: n2.ID, Name: "n2", Usage: panel.Usage{Down: 50}},
	}
	if !slices.Equal(got.NodeTraffic, wantNodes) {
		t.Errorf("node traffic = %+v, want %+v", got.NodeTraffic, wantNodes)
	}

	// alice has used 450 of 500 bytes, bob expires in two days, n2 is offline; n1 and carol need nothing.
	var attentionUsers []string
	for _, u := range got.AttentionUsers {
		attentionUsers = append(attentionUsers, u.Name)
	}
	if want := []string{"alice", "bob"}; !slices.Equal(attentionUsers, want) {
		t.Errorf("attention users = %v, want %v", attentionUsers, want)
	}
	if len(got.AttentionNodes) != 1 || got.AttentionNodes[0].Name != "n2" {
		t.Errorf("attention nodes = %+v, want n2", got.AttentionNodes)
	}
}
