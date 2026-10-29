package admin

import (
	"context"
	"errors"
	"github.com/artemlavr/giocondo/internal/dns"
	"github.com/artemlavr/giocondo/internal/healthcheck"
	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
	"testing"
	"time"
)

type memoryDNS struct {
	r    dns.Record
	fail bool
}

func (d *memoryDNS) Lookup(context.Context, string) ([]dns.Record, error) {
	if d.r.ID == "" {
		return nil, nil
	}
	return []dns.Record{d.r}, nil
}
func (d *memoryDNS) Delete(context.Context, string) error { return nil }
func (d *memoryDNS) Ensure(_ context.Context, host, ip, id string) (dns.Record, error) {
	if d.fail {
		return dns.Record{}, errors.New("DNS unavailable")
	}
	d.r = dns.Record{ID: "r1", Name: host, Content: ip, TTL: 60, Type: "A"}
	return d.r, nil
}
func TestManagedDNSAndHealthThreshold(t *testing.T) {
	ctx := context.Background()
	clk := newClock(time.Now())
	s, _ := newService(t, clk)
	d := &memoryDNS{}
	s.DNS = d
	s.DNSDomain = "vpn.example.com"
	n, err := s.CreateNode(ctx, CreateNodeInput{Name: "n1", Host: "192.0.2.1", IP: "192.0.2.1", VLESS: &VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnableDNS(ctx, n.ID, false); err == nil {
		t.Fatal("must require confirmation")
	}
	if _, err = s.EnableDNS(ctx, n.ID, true); err != nil {
		t.Fatal(err)
	}
	updated, _ := s.Store.GetNode(ctx, n.ID)
	if updated.Host != "n1.vpn.example.com" || !updated.ManagedDNS {
		t.Fatal(updated)
	}
	if _, err = s.UpdateNode(ctx, n.ID, UpdateNodeInput{IP: ptr("192.0.2.9")}); err == nil {
		t.Fatal("managed IP bypass")
	}
	_ = s.Store.TouchNode(ctx, n.ID, store.NodeOnline, clk.now())
	ev := &events{}
	s.Notify = ev.add
	s.Checker = healthcheck.CheckerFunc(func(context.Context, healthcheck.Request) error { return errors.New("blocked") })
	for i := 0; i < 2; i++ {
		if err = s.CheckNodesOnce(ctx); err != nil {
			t.Fatal(err)
		}
		expectEvents(t, ev, "below threshold")
	}
	if err = s.CheckNodesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	expectEvents(t, ev, "third", "node_down:n1")
	if err = s.CheckNodesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	expectEvents(t, ev, "deduplicated")
	s.Checker = healthcheck.CheckerFunc(func(context.Context, healthcheck.Request) error { return nil })
	if err = s.CheckNodesOnce(ctx); err != nil {
		t.Fatal(err)
	}
	expectEvents(t, ev, "recovered", "node_up:n1")
}
func ptr(s string) *string { return &s }
func TestRotateExistingPreservesIdentity(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	d := &memoryDNS{}
	s.DNS = d
	s.DNSDomain = "vpn.example.com"
	s.Checker = healthcheck.CheckerFunc(func(context.Context, healthcheck.Request) error { return nil })
	n, err := s.CreateNode(ctx, CreateNodeInput{Name: "n1", Host: "192.0.2.1", IP: "192.0.2.1", VLESS: &VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnableDNS(ctx, n.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddAddress(ctx, n.ID, "192.0.2.2"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Store.ListInbounds(ctx, n.ID)
	op, err := s.RotateIP(ctx, n.ID, RotateIPInput{Mode: "existing_ip", IP: "192.0.2.2"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		v, err := s.Store.GetNetworkOperation(ctx, op.ID)
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
	after, _ := s.Store.ListInbounds(ctx, n.ID)
	now, _ := s.Store.GetNode(ctx, n.ID)
	if now.IP != "192.0.2.2" || now.Host != "n1.vpn.example.com" || before[0].Spec.Reality.PrivateKey != after[0].Spec.Reality.PrivateKey {
		t.Fatalf("identity changed: %+v", now)
	}
	d.fail = true
	op, err = s.RotateIP(ctx, n.ID, RotateIPInput{Mode: "existing_ip", IP: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		v, _ := s.Store.GetNetworkOperation(ctx, op.ID)
		if v.Status != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	now, _ = s.Store.GetNode(ctx, n.ID)
	if now.IP != "192.0.2.2" {
		t.Fatal("failed DNS update changed IP")
	}
	_ = singbox.TypeVLESSReality
}

func TestRotateRefusesUnreconciledDNS(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	d := &memoryDNS{}
	s.DNS = d
	s.DNSDomain = "vpn.example.com"
	s.Checker = healthcheck.CheckerFunc(func(context.Context, healthcheck.Request) error { return nil })
	n, err := s.CreateNode(ctx, CreateNodeInput{Name: "n1", Host: "192.0.2.1", IP: "192.0.2.1", VLESS: &VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EnableDNS(ctx, n.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddAddress(ctx, n.ID, "192.0.2.2"); err != nil {
		t.Fatal(err)
	}
	d.r.Content = "192.0.2.9"
	if _, err = s.RotateIP(ctx, n.ID, RotateIPInput{Mode: "existing_ip", IP: "192.0.2.2"}); err == nil {
		t.Fatal("rotation accepted divergent DNS state")
	}
}

func TestHealthEventIgnoresRotatedAddressSnapshot(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	ev := &events{}
	s.Notify = ev.add
	n := &store.Node{Name: "n", Host: "n.example.com", IP: "192.0.2.1", Status: store.NodeOnline}
	_ = s.Store.CreateNode(ctx, n)
	for i := 0; i < 3; i++ {
		_, _ = s.Store.RecordProbe(ctx, store.ProbeResult{NodeID: n.ID, IP: n.IP, Tag: "vless", Point: "panel", Error: "blocked", CheckedAt: s.now()})
	}
	stale := *n
	n.IP = "192.0.2.2"
	_ = s.Store.UpdateNode(ctx, n)
	if err := s.healthEvent(ctx, stale); err != nil {
		t.Fatal(err)
	}
	expectEvents(t, ev, "old address must not trigger an alert after rotation")
}

func TestResumeRejectsSupersededInstallation(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	s.DNS = &memoryDNS{}
	s.Checker = healthcheck.CheckerFunc(func(context.Context, healthcheck.Request) error { return nil })
	n := &store.Node{Name: "n", Host: "n.example.com", IP: "192.0.2.2"}
	_ = s.Store.CreateNode(ctx, n)
	installs := []*store.Installation{{NodeID: n.ID, Identity: "a", IP: "192.0.2.1"}, {NodeID: n.ID, Identity: "old-b", IP: n.IP}, {NodeID: n.ID, Identity: "fresh-b", IP: n.IP}}
	for _, i := range installs {
		if err := s.Store.CreateInstallation(ctx, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Store.ActivateInstallation(ctx, n.ID, installs[2].ID, n.IP); err != nil {
		t.Fatal(err)
	}
	op := &store.NetworkOperation{ID: "historical", NodeID: n.ID, Kind: "ssh", OldIP: installs[0].IP, NewIP: n.IP, OldInstallation: installs[0].ID, NewInstallation: installs[1].ID}
	_ = s.Store.CreateNetworkOperation(ctx, op)
	op.Status = "ok"
	_ = s.Store.SaveNetworkOperation(ctx, op)
	if _, err := s.ResumeIP(ctx, op.ID, "retry", ResumeIPInput{}); err == nil {
		t.Fatal("historical operation reactivated retired generation")
	}
}
