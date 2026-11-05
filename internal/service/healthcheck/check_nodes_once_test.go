package healthcheck

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// fakeStore is an in-memory node, address and probe store. inboundErr fails ListInbounds of a node.
type fakeStore struct {
	nodes      []panel.Node
	inbounds   map[int64][]panel.Inbound
	inboundErr map[int64]error
	probes     []panel.ProbeResult
}

func (f *fakeStore) GetNode(_ context.Context, id int64) (*panel.Node, error) {
	for _, n := range f.nodes {
		if n.ID == id {
			return &n, nil
		}
	}

	return nil, panel.ErrNotFound
}

func (f *fakeStore) ListNodes(context.Context) ([]panel.Node, error) { return f.nodes, nil }

func (f *fakeStore) ListInbounds(_ context.Context, nodeID int64) ([]panel.Inbound, error) {
	if err := f.inboundErr[nodeID]; err != nil {
		return nil, err
	}

	return f.inbounds[nodeID], nil
}

func (f *fakeStore) SetNodeDownNotified(context.Context, int64, bool) error { return nil }

func (f *fakeStore) ListAddresses(context.Context, int64) ([]panel.Address, error) { return nil, nil }

func (f *fakeStore) ListProbes(_ context.Context, nodeID int64) ([]panel.ProbeResult, error) {
	var out []panel.ProbeResult

	for _, p := range f.probes {
		if p.NodeID == nodeID {
			out = append(out, p)
		}
	}

	return out, nil
}

func (f *fakeStore) RecordProbe(_ context.Context, p panel.ProbeResult) (panel.ProbeResult, error) {
	f.probes = append(f.probes, p)

	return p, nil
}

type fakeMonitor struct{}

func (fakeMonitor) EnsureMonitor(context.Context, int64) (*protocol.User, error) {
	return &protocol.User{Name: "monitor", UUID: "00000000-0000-0000-0000-000000000001"}, nil
}

type directProber struct{}

func (directProber) CheckProtocol(ctx context.Context, c probe.Checker, r probe.Request) error {
	return c.Check(ctx, r)
}

type fakeViews struct{}

func (fakeViews) GetNode(context.Context, int64) (panel.NodeView, error) {
	return panel.NodeView{}, nil
}

func runSequentially(ctx context.Context, probes []func(context.Context)) error {
	for _, p := range probes {
		p(ctx)
	}

	return nil
}

func TestCheckNodesOnceIsolatesNodeFailures(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	errInbounds := errors.New("inbounds unavailable")
	vless := protocol.Inbound{Type: "vless", Port: 443}

	store := &fakeStore{
		nodes: []panel.Node{
			{ID: 1, Name: "broken", Host: "a.example.com", IP: "192.0.2.1", Status: panel.NodeOnline},
			{ID: 2, Name: "healthy", Host: "b.example.com", IP: "192.0.2.2", Status: panel.NodeOnline},
		},
		inbounds:   map[int64][]panel.Inbound{2: {{ID: 1, NodeID: 2, Spec: vless}}},
		inboundErr: map[int64]error{1: errInbounds},
	}
	hooks := operation.Hooks{
		Now:  func() time.Time { return now },
		Emit: func(panel.Event) {},
	}
	healthy := probe.CheckerFunc(func(context.Context, probe.Request) error { return nil })

	s := New(hooks, store, store, store, healthy, nil, runSequentially, 0,
		new(operation.NodeStateLock), fakeMonitor{}, directProber{}, fakeViews{})

	err := s.CheckNodesOnce(context.Background())
	if !errors.Is(err, errInbounds) || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("CheckNodesOnce error = %v, want the inbound error of node broken", err)
	}

	want := panel.ProbeResult{NodeID: 2, IP: "192.0.2.2", Tag: vless.EffectiveTag(), Point: "panel", CheckedAt: now}
	if len(store.probes) != 1 || store.probes[0] != want {
		t.Fatalf("recorded probes = %+v, want only %+v", store.probes, want)
	}
}
