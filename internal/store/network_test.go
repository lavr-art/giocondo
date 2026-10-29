package store

import (
	"context"
	"testing"
	"time"
)

func TestNetworkStateAndOperationLock(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/test.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	n := &Node{Name: "n1", Host: "192.0.2.1", IP: "192.0.2.1"}
	if err = s.CreateNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	a := &Address{NodeID: n.ID, IP: "192.0.2.1"}
	if err = s.CreateAddress(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateAddress(ctx, &Address{NodeID: n.ID, IP: a.IP}); err == nil {
		t.Fatal("duplicate address accepted")
	}
	op := &NetworkOperation{ID: "one", NodeID: n.ID, Kind: "existing_ip", Stage: "preparing", OldIP: a.IP, NewIP: "192.0.2.2"}
	if err = s.CreateNetworkOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateNetworkOperation(ctx, &NetworkOperation{ID: "two", NodeID: n.ID, Kind: "ssh", Stage: "preparing"}); err == nil {
		t.Fatal("parallel operation accepted")
	}
	for i := 1; i <= 3; i++ {
		h, err := s.RecordProbe(ctx, ProbeResult{NodeID: n.ID, IP: a.IP, Tag: "hy2", Point: "panel", Error: "timeout", CheckedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if h.Failures != i {
			t.Fatalf("failures %d", h.Failures)
		}
	}
	h, err := s.RecordProbe(ctx, ProbeResult{NodeID: n.ID, IP: a.IP, Tag: "hy2", Point: "panel", CheckedAt: time.Now()})
	if err != nil || h.Failures != 0 {
		t.Fatalf("reset %+v %v", h, err)
	}
	op.Status = "failed"
	if err = s.SaveNetworkOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateNetworkOperation(ctx, &NetworkOperation{ID: "two", NodeID: n.ID, Kind: "ssh", Stage: "preparing"}); err != nil {
		t.Fatal(err)
	}
}

func TestAdoptingInstallationPreservesAppliedHash(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	n := &Node{Name: "legacy", Host: "legacy.example.com"}
	_ = s.CreateNode(ctx, n)
	_ = s.SetNodeApplied(ctx, n.ID, "existing-hash", "")
	i := &Installation{NodeID: n.ID, Identity: n.Name, State: "active"}
	_ = s.CreateInstallation(ctx, i)
	if err = s.ActivateInstallation(ctx, n.ID, i.ID, "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetNode(ctx, n.ID)
	if got.AppliedHash != "existing-hash" {
		t.Fatalf("legacy applied hash lost: %q", got.AppliedHash)
	}
}

func TestActivationReassignsPreviouslyUsedIP(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir()+"/db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	n := &Node{Name: "n", Host: "n.example.com", IP: "192.0.2.1"}
	_ = s.CreateNode(ctx, n)
	first := &Installation{NodeID: n.ID, Identity: "first", IP: n.IP}
	_ = s.CreateInstallation(ctx, first)
	_ = s.ActivateInstallation(ctx, n.ID, first.ID, n.IP)
	second := &Installation{NodeID: n.ID, Identity: "second", IP: n.IP}
	_ = s.CreateInstallation(ctx, second)
	if err = s.ActivateInstallation(ctx, n.ID, second.ID, n.IP); err != nil {
		t.Fatal(err)
	}
	addresses, err := s.ListAddresses(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(addresses) != 1 || addresses[0].InstallationID != second.ID {
		t.Fatalf("stale address binding: %+v", addresses)
	}
}
