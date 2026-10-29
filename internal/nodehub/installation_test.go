package nodehub

import (
	"context"
	"github.com/artemlavr/giocondo/internal/store"
	"testing"
	"time"
)

func TestInstallationCannotOverwriteActiveNode(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, t.TempDir()+"/db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n := &store.Node{Name: "n", Host: "n.example.com", IP: "192.0.2.1"}
	_ = s.CreateNode(ctx, n)
	old := &store.Installation{NodeID: n.ID, Identity: "n", IP: n.IP}
	_ = s.CreateInstallation(ctx, old)
	_ = s.ActivateInstallation(ctx, n.ID, old.ID, n.IP)
	candidate := &store.Installation{NodeID: n.ID, Identity: "candidate", IP: "192.0.2.2"}
	_ = s.CreateInstallation(ctx, candidate)
	_ = s.TouchInstallation(ctx, candidate.ID, store.NodeOnline, time.Now())
	_ = s.ApplyInstallation(ctx, candidate.ID, "new", "")
	got, _ := s.GetNode(ctx, n.ID)
	if got.AppliedHash != "" || got.Status == store.NodeOnline {
		t.Fatal("candidate overwrote active node")
	}
	_ = s.ActivateInstallation(ctx, n.ID, candidate.ID, candidate.IP)
	_ = s.TouchInstallation(ctx, old.ID, store.NodeOffline, time.Now())
	_ = s.ApplyInstallation(ctx, old.ID, "old", "")
	got, _ = s.GetNode(ctx, n.ID)
	if got.AppliedHash != "new" || got.IP != candidate.IP {
		t.Fatal("old installation overwrote new one")
	}
}
