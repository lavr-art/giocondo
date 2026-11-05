package panelintegration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	. "github.com/artemlavr/giocondo/test/paneltest" //nolint:revive // dot import keeps the test package call sites concise.
)

type unavailableMonitorStore struct{ NodeRepository }

func (s unavailableMonitorStore) MonitorCredentials(context.Context, int64) (*protocol.User, error) {
	return nil, errors.New("monitor storage unavailable")
}

func TestCreateNodeMonitorPreparationDoesNotUndoCreation(t *testing.T) {
	svc, kicks := newService(t, newClock(time.Now()))
	svc.configure(t, func(_ *Config, deps *Dependencies) {
		deps.Repositories.Nodes = unavailableMonitorStore{svc.Store.Nodes}
	})
	svc.configure(t, func(_ *Config, deps *Dependencies) {
		deps.Checker = probe.CheckerFunc(func(context.Context, probe.Request) error { return nil })
	})
	n, err := svc.Nodes.CreateNode(context.Background(), panel.CreateNodeInput{Name: "created", Host: "example.com", VLESS: &panel.VLESSInput{}})
	if err != nil {
		t.Fatalf("committed node reported as failed: %v", err)
	}
	saved, err := svc.Store.Nodes.GetNode(context.Background(), n.ID)
	if err != nil || saved.Name != "created" {
		t.Fatalf("saved node: %+v, %v", saved, err)
	}
	ins, err := svc.Store.Nodes.ListInbounds(context.Background(), n.ID)
	if err != nil || len(ins) != 1 {
		t.Fatalf("protocols: %v, %v", ins, err)
	}
	if kicks.Load() != 1 {
		t.Fatalf("config notifications: %d", kicks.Load())
	}
}

func TestEnsureMonitorOnlyNotifiesOnCreation(t *testing.T) {
	ctx := context.Background()
	s, kicks := newService(t, newClock(time.Now()))
	n, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{Name: "monitor", Host: "example.com", VLESS: &panel.VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	baseline := kicks.Load()
	first, err := s.Credentials.EnsureMonitor(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Credentials.EnsureMonitor(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.UUID != second.UUID {
		t.Fatal("existing monitor credentials changed")
	}
	if got := kicks.Load() - baseline; got != 1 {
		t.Fatalf("notifications: %d, want one for the new credentials", got)
	}
}
