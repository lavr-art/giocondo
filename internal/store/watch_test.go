package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUserWatchKeptByUpdate(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	u := mustUser(t, s, User{Name: "alice", Enabled: true})
	w := UserWatch{LastStatus: "active", WarnedTraffic: true}
	if err := s.SetUserWatch(ctx, u.ID, w); err != nil {
		t.Fatal(err)
	}
	// UpdateUser does not touch service fields even if they are empty in the struct.
	u.TrafficLimitBytes = 10
	if err := s.UpdateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetUser(ctx, u.ID)
	if got.Watch != w {
		t.Errorf("watch = %+v, want %+v", got.Watch, w)
	}
	if err := s.SetUserWatch(ctx, 999, w); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
}

func TestNodeSyncState(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	n := mustNode(t, s, Node{Name: "n1", Host: "n1.example.com"})
	l := mustNode(t, s, Node{Name: "local", Host: "vpn.example.com", Runtime: RuntimeLocal})
	if n.Runtime != RuntimeRemote {
		t.Errorf("default runtime %q", n.Runtime)
	}
	if got, _ := s.GetNode(ctx, l.ID); got.Runtime != RuntimeLocal {
		t.Errorf("runtime %q", got.Runtime)
	}

	if got, _ := s.GetNode(ctx, n.ID); got.Dirty() {
		t.Error("node without desired config is dirty")
	}
	if err := s.SetNodeDesired(ctx, n.ID, "aaa"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetNode(ctx, n.ID); !got.Dirty() {
		t.Error("node is not dirty after new desired config")
	}
	if err := s.SetNodeApplied(ctx, n.ID, "old", "check failed"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetNode(ctx, n.ID)
	if !got.Dirty() || got.ApplyError != "check failed" {
		t.Errorf("after failed apply %+v", got)
	}
	if err := s.SetNodeApplied(ctx, n.ID, "aaa", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetNodeDownNotified(ctx, n.ID, true); err != nil {
		t.Fatal(err)
	}
	// UpdateNode does not touch service fields.
	got, _ = s.GetNode(ctx, n.ID)
	got.Region = "hel1"
	got.DesiredHash, got.DownNotified = "zzz", false
	if err := s.UpdateNode(ctx, got); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetNode(ctx, n.ID)
	if got.Dirty() || got.ApplyError != "" || !got.DownNotified || got.Region != "hel1" {
		t.Errorf("after apply and update %+v", got)
	}
}

func TestTokens(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	tok := &APIToken{Name: "bot", Scope: "monitor", Hash: "h1"}
	if err := s.CreateToken(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateToken(ctx, &APIToken{Name: "dup", Scope: "admin", Hash: "h1"}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate hash: %v", err)
	}
	got, err := s.TokenByHash(ctx, "h1")
	if err != nil || got.ID != tok.ID || got.Scope != "monitor" || got.LastUsedAt != nil {
		t.Fatalf("by hash %+v %v", got, err)
	}
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if err := s.TouchToken(ctx, tok.ID, at); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListTokens(ctx)
	if len(list) != 1 || list[0].LastUsedAt == nil || !list[0].LastUsedAt.Equal(at) {
		t.Errorf("list %+v", list)
	}
	if err := s.DeleteToken(ctx, tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TokenByHash(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted token: %v", err)
	}
}
