package nodestate_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
	"github.com/artemlavr/giocondo/internal/repository/sqlite/network"
	"github.com/artemlavr/giocondo/internal/repository/sqlite/nodes"
	"github.com/artemlavr/giocondo/internal/repository/sqlite/traffic"
	"github.com/artemlavr/giocondo/internal/repository/sqlite/users"
	"github.com/artemlavr/giocondo/internal/service/nodeconfig"
	"github.com/artemlavr/giocondo/internal/service/nodestate"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/internal/service/singbox"
)

var now = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

type fixture struct {
	nodes   *nodes.Repository
	users   *users.Repository
	network *network.Repository
	traffic *traffic.Repository
	svc     *nodestate.Service
	node    *panel.Node
}

// newFixture creates node n1 with VLESS Reality, Hysteria2 and WireGuard inbounds and user alice.
func newFixture(t *testing.T) *fixture {
	t.Helper()

	ctx := context.Background()

	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	f := &fixture{nodes: nodes.New(db), users: users.New(db), network: network.New(db), traffic: traffic.New(db)}

	spec := protocol.Node{Host: "127.0.0.1", Inbounds: []protocol.Inbound{
		{Type: protocol.TypeVLESSReality, Tag: "vless", Port: 443, Reality: &protocol.RealityParams{Dest: "www.microsoft.com:443", ServerName: "www.microsoft.com"}},
		{Type: protocol.TypeHysteria2, Tag: "hy2", Port: 8443},
		{Type: protocol.TypeWireGuard, Tag: "wg", Port: 51820},
	}}
	if err := singbox.Fill(&spec, nil); err != nil {
		t.Fatal(err)
	}

	n := &panel.Node{Name: "n1", Host: spec.Host}
	if err := f.nodes.CreateNode(ctx, n); err != nil {
		t.Fatal(err)
	}

	for _, in := range spec.Inbounds {
		if err := f.nodes.CreateInbound(ctx, &panel.Inbound{NodeID: n.ID, Spec: in}); err != nil {
			t.Fatal(err)
		}
	}

	if err := f.users.CreateUser(ctx, &panel.User{Name: "alice", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	svc := nodestate.New(
		operation.Hooks{Now: func() time.Time { return now }},
		f.nodes,
		f.users,
		f.traffic,
		f.network,
		&nodeconfig.Allocator{},
		singbox.Options{},
	)

	f.svc, f.node = svc, n

	return f
}

func (f *fixture) installation(t *testing.T, identity, ip string, activate bool) *panel.Installation {
	t.Helper()

	ctx := context.Background()

	i := &panel.Installation{NodeID: f.node.ID, Identity: identity, IP: ip}
	if err := f.network.CreateInstallation(ctx, i); err != nil {
		t.Fatal(err)
	}

	if activate {
		if err := f.network.ActivateInstallation(ctx, f.node.ID, i.ID, ip); err != nil {
			t.Fatal(err)
		}
	}

	return i
}

func (f *fixture) storedNode(t *testing.T) *panel.Node {
	t.Helper()

	n, err := f.nodes.GetNode(context.Background(), f.node.ID)
	if err != nil {
		t.Fatal(err)
	}

	return n
}

func TestAdmit(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name     string
		prepare  func(t *testing.T, f *fixture)
		identity string
		wantErr  error
		wantInst bool
	}{
		{name: "registered node", identity: "n1"},
		{name: "unknown identity", identity: "ghost", wantErr: nodestate.ErrNotRegistered},
		{
			name: "disabled node", identity: "n1", wantErr: nodestate.ErrDisabled,
			prepare: func(t *testing.T, f *fixture) {
				n := f.storedNode(t)
				n.Status = panel.NodeDisabled
				if err := f.nodes.UpdateNode(ctx, n); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "installation identity", identity: "inst-a", wantInst: true,
			prepare: func(t *testing.T, f *fixture) { f.installation(t, "inst-a", "192.0.2.10", true) },
		},
		{
			name: "retired installation", identity: "inst-a", wantErr: nodestate.ErrRetired,
			prepare: func(t *testing.T, f *fixture) {
				i := f.installation(t, "inst-a", "192.0.2.10", true)
				if err := f.network.SetInstallationState(ctx, i.ID, panel.InstallationRetired); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			if tc.prepare != nil {
				tc.prepare(t, f)
			}

			a, err := f.svc.Admit(ctx, tc.identity)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Admit(%q) err = %v, want %v", tc.identity, err, tc.wantErr)
			}

			if tc.wantErr != nil {
				return
			}

			if a.Node.ID != f.node.ID || a.Identity != tc.identity || (a.Installation != nil) != tc.wantInst {
				t.Fatalf("agent = %+v, want node %d with installation %v", a, f.node.ID, tc.wantInst)
			}
		})
	}
}

// Until the identity is an installation the node itself carries the reported state; afterwards
// only the installation does, and a candidate must not overwrite the active node.
func TestRecordAppliedTargetsNodeOrInstallation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	a, err := f.svc.Admit(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}

	f.svc.RecordApplied(ctx, a, "hash-1", "boom")

	if n := f.storedNode(t); n.AppliedHash != "hash-1" || n.ApplyError != "boom" {
		t.Fatalf("node applied = %q/%q, want hash-1/boom", n.AppliedHash, n.ApplyError)
	}

	f.installation(t, "n1", "192.0.2.10", true)
	candidate := f.installation(t, "candidate", "192.0.2.20", false)

	f.svc.RecordApplied(ctx, a, "hash-2", "")

	if a.Installation == nil {
		t.Fatal("installation registered during the stream was not resolved")
	}

	if n := f.storedNode(t); n.AppliedHash != "hash-2" || n.ApplyError != "" {
		t.Fatalf("active installation applied = %q/%q, want hash-2 without error", n.AppliedHash, n.ApplyError)
	}

	ca, err := f.svc.Admit(ctx, candidate.Identity)
	if err != nil {
		t.Fatal(err)
	}

	f.svc.RecordApplied(ctx, ca, "candidate-hash", "")

	if n := f.storedNode(t); n.AppliedHash != "hash-2" {
		t.Fatalf("candidate overwrote the node: applied = %q", n.AppliedHash)
	}
}

func TestCheckRetired(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	i := f.installation(t, "n1", "192.0.2.10", true)

	a, err := f.svc.Admit(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}

	if err := f.svc.CheckRetired(ctx, a); err != nil {
		t.Fatalf("active installation: %v", err)
	}

	if err := f.network.SetInstallationState(ctx, i.ID, panel.InstallationRetired); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.CheckRetired(ctx, a); !errors.Is(err, nodestate.ErrRetired) {
		t.Fatalf("retired installation: err = %v, want ErrRetired", err)
	}
}

// A batch is deduplicated by agent instance and seq; unknown users and negative deltas are skipped.
func TestRecordStats(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	a, err := f.svc.Admit(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}

	batch := []nodestate.UserTraffic{{Name: "alice", Up: 10, Down: 3}, {Name: "ghost", Up: 100}, {Name: "alice", Up: -1}}
	for range 3 {
		if err := f.svc.RecordStats(ctx, a, "inst-a", 1, batch); err != nil {
			t.Fatal(err)
		}
	}

	// A new agent process restarts seq: this is new data.
	if err := f.svc.RecordStats(ctx, a, "inst-b", 1, batch); err != nil {
		t.Fatal(err)
	}

	u, err := f.traffic.UsageByUser(ctx, 1, now, now)
	if err != nil {
		t.Fatal(err)
	}

	if u.Up != 20 || u.Down != 6 {
		t.Fatalf("usage = %d/%d, want 20/6 (one batch per agent instance)", u.Up, u.Down)
	}
}

func TestNetworkConfig(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	a, err := f.svc.Admit(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}

	if _, ok, err := f.svc.NetworkConfig(ctx, a); err != nil || ok {
		t.Fatalf("without an active installation: ok = %v, err = %v, want nothing to send", ok, err)
	}

	active := f.installation(t, "n1", "192.0.2.10", true)
	other := f.installation(t, "spare", "192.0.2.20", false)

	for _, addr := range []*panel.Address{
		{NodeID: f.node.ID, InstallationID: active.ID, IP: "192.0.2.11"},
		{NodeID: f.node.ID, InstallationID: other.ID, IP: "192.0.2.21"},
	} {
		if err := f.network.CreateAddress(ctx, addr); err != nil {
			t.Fatal(err)
		}
	}

	got, ok, err := f.svc.NetworkConfig(ctx, a)
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}

	// Activation records the primary IP as an address of the installation, so it is listed too;
	// the other installation's address is not.
	want := nodestate.Network{PrimaryIP: "192.0.2.10", Aliases: []string{"192.0.2.10", "192.0.2.11"}, UDPPorts: []uint32{8443, 51820}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("network = %+v, want %+v", got, want)
	}
}

// The desired hash is written when it changes; a cached hash is rewritten after ReconcileOnce sees
// the database disagree.
func TestDesiredConfigRecordsHash(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	_, hash, err := f.svc.DesiredConfig(ctx, f.node.ID)
	if err != nil {
		t.Fatal(err)
	}

	if n := f.storedNode(t); n.DesiredHash != hash {
		t.Fatalf("desired_hash = %q, want %q", n.DesiredHash, hash)
	}

	if err := f.nodes.SetNodeDesired(ctx, f.node.ID, "stale"); err != nil {
		t.Fatal(err)
	}

	f.svc.ReconcileOnce(ctx)

	if n := f.storedNode(t); n.DesiredHash != hash {
		t.Fatalf("after reconcile desired_hash = %q, want %q", n.DesiredHash, hash)
	}
}

func TestDesiredConfigRequiresAllocator(t *testing.T) {
	f := newFixture(t)
	f.svc = nodestate.New(operation.Hooks{}, f.nodes, f.users, f.traffic, f.network, nil, singbox.Options{})

	if _, _, err := f.svc.DesiredConfig(context.Background(), f.node.ID); err == nil ||
		!strings.Contains(err.Error(), "no WireGuard allocator") {
		t.Fatalf("DesiredConfig without an allocator: err = %v, want the missing allocator", err)
	}
}
