package panelintegration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	. "github.com/artemlavr/giocondo/test/paneltest" //nolint:revive // dot import keeps the test package call sites concise.
)

type memoryDNS struct {
	r    panel.DNSRecord
	fail bool
}

func (d *memoryDNS) Lookup(context.Context, string) ([]panel.DNSRecord, error) {
	if d.r.ID == "" {
		return nil, nil
	}
	return []panel.DNSRecord{d.r}, nil
}

func (d *memoryDNS) Delete(context.Context, string) error { return nil }

func (d *memoryDNS) Ensure(_ context.Context, host, ip, _ string) (panel.DNSRecord, error) {
	if d.fail {
		return panel.DNSRecord{}, errors.New("DNS unavailable")
	}
	d.r = panel.DNSRecord{ID: "r1", Name: host, Content: ip, TTL: 60, Type: "A"}
	return d.r, nil
}

func TestManagedDNSAndHealthThreshold(t *testing.T) {
	ctx := context.Background()
	clk := newClock(time.Now())
	s, _ := newService(t, clk)
	d := &memoryDNS{}
	s.configure(t, func(_ *Config, deps *Dependencies) { deps.DNS = d })
	s.configure(t, func(cfg *Config, _ *Dependencies) { cfg.DNSDomain = "vpn.example.com" })
	n, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{Name: "n1", Host: "192.0.2.1", IP: "192.0.2.1", VLESS: &panel.VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Network.EnableDNS(ctx, n.ID, false); err == nil {
		t.Fatal("must require confirmation")
	}
	if _, err = s.Network.EnableDNS(ctx, n.ID, true); err != nil {
		t.Fatal(err)
	}
	updated, _ := s.Store.Nodes.GetNode(ctx, n.ID)
	if updated.Host != "n1.vpn.example.com" || !updated.ManagedDNS {
		t.Fatal(updated)
	}
	if _, err = s.Nodes.UpdateNode(ctx, n.ID, panel.UpdateNodeInput{IP: ptr("192.0.2.9")}); err == nil {
		t.Fatal("managed IP bypass")
	}
	_ = s.Store.Nodes.TouchNode(ctx, n.ID, panel.NodeOnline, clk.now())
	ev := &events{}
	s.configure(t, func(_ *Config, deps *Dependencies) { deps.Notify = ev.add })
	s.configure(t, func(_ *Config, deps *Dependencies) {
		deps.Checker = probe.CheckerFunc(func(context.Context, probe.Request) error { return errors.New("blocked") })
	})
	for i := 0; i < 2; i++ {
		if err = s.Health.CheckNodesOnce(ctx); err != nil {
			t.Fatal(err)
		}
		expectEvents(t, ev, "below threshold")
	}
	if err = s.Health.CheckNodesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	expectEvents(t, ev, "third", "node_down:n1")
	if err = s.Health.CheckNodesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	expectEvents(t, ev, "deduplicated")
	s.configure(t, func(_ *Config, deps *Dependencies) {
		deps.Checker = probe.CheckerFunc(func(context.Context, probe.Request) error { return nil })
	})
	if err = s.Health.CheckNodesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	expectEvents(t, ev, "recovered", "node_up:n1")
}

func ptr(s string) *string { return &s }

func TestRotateExistingPreservesIdentity(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	d := &memoryDNS{}
	s.configure(t, func(_ *Config, deps *Dependencies) { deps.DNS = d })
	s.configure(t, func(cfg *Config, _ *Dependencies) { cfg.DNSDomain = "vpn.example.com" })
	s.configure(t, func(_ *Config, deps *Dependencies) {
		deps.Checker = probe.CheckerFunc(func(context.Context, probe.Request) error { return nil })
	})
	n, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{Name: "n1", Host: "192.0.2.1", IP: "192.0.2.1", VLESS: &panel.VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Network.EnableDNS(ctx, n.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Network.AddAddress(ctx, n.ID, "192.0.2.2"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Store.Nodes.ListInbounds(ctx, n.ID)
	op, err := s.Network.RotateIP(ctx, n.ID, panel.RotateIPInput{Mode: "existing_ip", IP: "192.0.2.2"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		v, err := s.Store.Network.GetNetworkOperation(ctx, op.ID)
		if err != nil {
			t.Fatal(err)
		}
		if v.Status != "running" {
			if v.Status != "ok" {
				t.Fatalf("%+v", v)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	after, _ := s.Store.Nodes.ListInbounds(ctx, n.ID)
	now, _ := s.Store.Nodes.GetNode(ctx, n.ID)
	if now.IP != "192.0.2.2" || now.Host != "n1.vpn.example.com" || before[0].Spec.Reality.PrivateKey != after[0].Spec.Reality.PrivateKey {
		t.Fatalf("identity changed: %+v", now)
	}
	d.fail = true
	op, err = s.Network.RotateIP(ctx, n.ID, panel.RotateIPInput{Mode: "existing_ip", IP: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		v, _ := s.Store.Network.GetNetworkOperation(ctx, op.ID)
		if v.Status != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	now, _ = s.Store.Nodes.GetNode(ctx, n.ID)
	if now.IP != "192.0.2.2" {
		t.Fatal("failed DNS update changed IP")
	}
	_ = protocol.TypeVLESSReality
}

func TestRotateRefusesUnreconciledDNS(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	d := &memoryDNS{}
	s.configure(t, func(_ *Config, deps *Dependencies) { deps.DNS = d })
	s.configure(t, func(cfg *Config, _ *Dependencies) { cfg.DNSDomain = "vpn.example.com" })
	s.configure(t, func(_ *Config, deps *Dependencies) {
		deps.Checker = probe.CheckerFunc(func(context.Context, probe.Request) error { return nil })
	})
	n, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{Name: "n1", Host: "192.0.2.1", IP: "192.0.2.1", VLESS: &panel.VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Network.EnableDNS(ctx, n.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Network.AddAddress(ctx, n.ID, "192.0.2.2"); err != nil {
		t.Fatal(err)
	}
	d.r.Content = "192.0.2.9"
	if _, err = s.Network.RotateIP(ctx, n.ID, panel.RotateIPInput{Mode: "existing_ip", IP: "192.0.2.2"}); err == nil {
		t.Fatal("rotation accepted divergent DNS state")
	}
}

func TestHealthEventIgnoresRotatedAddressSnapshot(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	ev := &events{}
	s.configure(t, func(_ *Config, deps *Dependencies) { deps.Notify = ev.add })
	n := &panel.Node{Name: "n", Host: "n.example.com", IP: "192.0.2.1", Status: panel.NodeOnline}
	_ = s.Store.Nodes.CreateNode(ctx, n)
	for i := 0; i < 3; i++ {
		_, _ = s.Store.Probes.RecordProbe(ctx, panel.ProbeResult{NodeID: n.ID, IP: n.IP, Tag: "vless", Point: "panel", Error: "blocked", CheckedAt: s.now()})
	}
	stale := *n
	n.IP = "192.0.2.2"
	_ = s.Store.Nodes.UpdateNode(ctx, n)
	if err := s.Health.HealthEvent(ctx, stale); err != nil {
		t.Fatal(err)
	}
	expectEvents(t, ev, "old address must not trigger an alert after rotation")
}

func TestResumeRejectsSupersededInstallation(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	s.configure(t, func(_ *Config, deps *Dependencies) { deps.DNS = &memoryDNS{} })
	s.configure(t, func(_ *Config, deps *Dependencies) {
		deps.Checker = probe.CheckerFunc(func(context.Context, probe.Request) error { return nil })
	})
	n := &panel.Node{Name: "n", Host: "n.example.com", IP: "192.0.2.2"}
	_ = s.Store.Nodes.CreateNode(ctx, n)
	installs := []*panel.Installation{{NodeID: n.ID, Identity: "a", IP: "192.0.2.1"}, {NodeID: n.ID, Identity: "old-b", IP: n.IP}, {NodeID: n.ID, Identity: "fresh-b", IP: n.IP}}
	for _, i := range installs {
		if err := s.Store.Network.CreateInstallation(ctx, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Store.Network.ActivateInstallation(ctx, n.ID, installs[2].ID, n.IP); err != nil {
		t.Fatal(err)
	}
	op := &panel.NetworkOperation{ID: "historical", NodeID: n.ID, Kind: "ssh", OldIP: installs[0].IP, NewIP: n.IP, OldInstallation: installs[0].ID, NewInstallation: installs[1].ID}
	_ = s.Store.Network.CreateNetworkOperation(ctx, op)
	op.Status = "ok"
	_ = s.Store.Network.SaveNetworkOperation(ctx, op)
	if _, err := s.Network.ResumeIP(ctx, op.ID, "retry", panel.ResumeIPInput{}); err == nil {
		t.Fatal("historical operation reactivated retired generation")
	}
}

// failedRotation prepares an SSH rotation that switched DNS and the node to a new installation
// but stopped before the old one was revoked, so it can be resumed with retry.
func failedRotation(t *testing.T, s *serviceFixture) *panel.NetworkOperation {
	t.Helper()

	ctx := context.Background()
	n, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{Name: "n1", Host: "n1.vpn.example.com", IP: "192.0.2.1", VLESS: &panel.VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	installs := []*panel.Installation{{NodeID: n.ID, Identity: "old", IP: "192.0.2.1"}, {NodeID: n.ID, Identity: "new", IP: "192.0.2.2"}}
	for _, i := range installs {
		if err = s.Store.Network.CreateInstallation(ctx, i); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Store.Network.ActivateInstallation(ctx, n.ID, installs[1].ID, installs[1].IP); err != nil {
		t.Fatal(err)
	}
	op := &panel.NetworkOperation{ID: "interrupted", NodeID: n.ID, Kind: "ssh", OldIP: installs[0].IP, NewIP: installs[1].IP,
		OldInstallation: installs[0].ID, NewInstallation: installs[1].ID}
	if err = s.Store.Network.CreateNetworkOperation(ctx, op); err != nil {
		t.Fatal(err)
	}
	op.Status = panel.OperationFailed
	if err = s.Store.Network.SaveNetworkOperation(ctx, op); err != nil {
		t.Fatal(err)
	}

	return op
}

func awaitOperation(t *testing.T, s *serviceFixture, id string, within time.Duration) panel.NetworkOperation {
	t.Helper()

	deadline := time.Now().Add(within)
	for {
		v, err := s.Store.Network.GetNetworkOperation(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if v.Status != panel.OperationRunning {
			return *v
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation still running after %v: stage %q", within, v.Stage)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestResumeRetryUsesConfiguredDrainDelay(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	s.configure(t, func(cfg *Config, deps *Dependencies) {
		cfg.DrainDelay = 10 * time.Millisecond
		deps.DNS = &memoryDNS{r: panel.DNSRecord{ID: "r1", Content: "192.0.2.2", TTL: 60, Type: "A"}}
		deps.Checker = probe.CheckerFunc(func(context.Context, probe.Request) error { return nil })
		deps.Remote = &waitingRemote{}
	})
	op := failedRotation(t, s)

	resumed, err := s.Network.ResumeIP(ctx, op.ID, "retry", panel.ResumeIPInput{})
	if err != nil {
		t.Fatal(err)
	}

	got := awaitOperation(t, s, resumed.ID, 3*time.Second)
	if got.Status != panel.OperationOK || got.Stage != "complete" {
		t.Fatalf("resumed operation: status %q stage %q error %q", got.Status, got.Stage, got.Error)
	}
}

func TestResumeStoresSanitizedError(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	s.configure(t, func(_ *Config, deps *Dependencies) {
		deps.DNS = &memoryDNS{r: panel.DNSRecord{ID: "r1", Content: "192.0.2.2", TTL: 60, Type: "A"}}
		deps.Checker = probe.CheckerFunc(func(context.Context, probe.Request) error {
			return errors.New("handshake failed\nremote said: " + strings.Repeat("x", 400))
		})
	})
	op := failedRotation(t, s)

	resumed, err := s.Network.ResumeIP(ctx, op.ID, "retry", panel.ResumeIPInput{})
	if err != nil {
		t.Fatal(err)
	}

	got := awaitOperation(t, s, resumed.ID, 3*time.Second)
	if got.Status != panel.OperationFailed {
		t.Fatalf("status %q, want failed", got.Status)
	}
	if !strings.HasPrefix(got.Error, "panel vless readiness: handshake failed remote said: ") || len(got.Error) != 300 {
		t.Fatalf("stored error is not sanitized like rotation errors (%d bytes): %q", len(got.Error), got.Error)
	}
}

func TestResumeRollbackRestoresSource(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	d := &memoryDNS{r: panel.DNSRecord{ID: "r1", Content: "192.0.2.2", TTL: 60, Type: "A"}}
	s.configure(t, func(_ *Config, deps *Dependencies) {
		deps.DNS = d
		deps.Checker = probe.CheckerFunc(func(context.Context, probe.Request) error { return nil })
	})
	op := failedRotation(t, s)

	resumed, err := s.Network.ResumeIP(ctx, op.ID, "rollback", panel.ResumeIPInput{})
	if err != nil {
		t.Fatal(err)
	}

	got := awaitOperation(t, s, resumed.ID, 3*time.Second)
	if got.Status != panel.OperationOK || got.Stage != "rolled_back" {
		t.Fatalf("rollback: status %q stage %q error %q", got.Status, got.Stage, got.Error)
	}
	n, err := s.Store.Nodes.GetNode(ctx, op.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	if n.IP != op.OldIP || n.ActiveInstallation != op.OldInstallation || d.r.Content != op.OldIP {
		t.Fatalf("source not restored: ip %q installation %d dns %q", n.IP, n.ActiveInstallation, d.r.Content)
	}
	installs, err := s.Store.Network.Installations(ctx, op.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range installs {
		if i.ID == op.NewInstallation && i.State != "retired" {
			t.Fatalf("target installation state %q, want retired", i.State)
		}
	}
}
