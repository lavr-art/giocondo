package panelintegration

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/service/operation"
	. "github.com/artemlavr/giocondo/test/paneltest" //nolint:revive // dot import keeps the test package call sites concise.
)

func awaitResult(t *testing.T, c <-chan error) {
	t.Helper()

	select {
	case err := <-c:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("operation did not complete (possible deadlock)")
	}
}

func TestPeriodicAndRotationWireGuardProbesSerialize(t *testing.T) {
	s, _ := newService(t, newClock(time.Now()))
	first := make(chan struct{})
	release := make(chan struct{})
	var calls, active, peak atomic.Int32
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	checker := probe.CheckerFunc(func(ctx context.Context, _ probe.Request) error {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		if calls.Add(1) == 1 {
			close(first)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	s.configure(t, func(_ *Config, d *Dependencies) { d.Checker = checker })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	n, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{Name: "wg", Host: "vpn.example.com", IP: "192.0.2.1", WireGuard: &panel.WGNodeInput{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Nodes.TouchNode(ctx, n.ID, panel.NodeOnline, s.now()); err != nil {
		t.Fatal(err)
	}
	node, err := s.Store.Nodes.GetNode(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	periodic := make(chan error, 1)
	go func() { periodic <- s.Health.CheckNodesOnce(ctx) }()
	select {
	case <-first:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	rotation := make(chan error, 1)
	started := make(chan struct{})
	go func() { close(started); rotation <- s.App.ProbeService().CheckAddress(ctx, *node, "192.0.2.2") }()
	<-started
	select {
	case err := <-rotation:
		t.Fatalf("rotation probe bypassed the running WG probe: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	awaitResult(t, periodic)
	awaitResult(t, rotation)
	if peak.Load() != 1 || calls.Load() != 2 {
		t.Fatalf("overlapping probes: peak=%d calls=%d", peak.Load(), calls.Load())
	}
}

type guardedReadStore struct {
	NodeRepository
	block   atomic.Bool
	entered chan struct{}
	release chan struct{}
	wrote   chan struct{}
}

func (s *guardedReadStore) GetNode(ctx context.Context, id int64) (*panel.Node, error) {
	if s.block.CompareAndSwap(true, false) {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.NodeRepository.GetNode(ctx, id)
}

func (s *guardedReadStore) UpdateNode(ctx context.Context, n *panel.Node) error {
	select {
	case s.wrote <- struct{}{}:
	default:
	}
	return s.NodeRepository.UpdateNode(ctx, n)
}

func TestNodeMutationWaitsForHealthStateTransition(t *testing.T) {
	s, _ := newService(t, newClock(time.Now()))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	n, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{Name: "n", Host: "vpn.example.com", VLESS: &panel.VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	st := &guardedReadStore{NodeRepository: s.Store.Nodes, entered: make(chan struct{}), release: make(chan struct{}), wrote: make(chan struct{}, 1)}
	t.Cleanup(func() {
		select {
		case <-st.release:
		default:
			close(st.release)
		}
	})
	s.configure(t, func(_ *Config, d *Dependencies) { d.Repositories.Nodes = st })
	snapshot, err := s.Store.Nodes.GetNode(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	st.block.Store(true)
	health := make(chan error, 1)
	go func() { health <- s.Health.HealthEvent(ctx, *snapshot) }()
	select {
	case <-st.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	mutation := make(chan error, 1)
	go func() {
		region := "new-region"
		_, err := s.Nodes.UpdateNode(ctx, n.ID, panel.UpdateNodeInput{Region: &region})
		mutation <- err
	}()
	select {
	case <-st.wrote:
		t.Fatal("node mutated during a protected health transition")
	case <-time.After(40 * time.Millisecond):
	}
	close(st.release)
	awaitResult(t, health)
	awaitResult(t, mutation)
	got, err := s.Queries.GetNode(ctx, n.ID)
	if err != nil || got.Region != "new-region" {
		t.Fatalf("updated node: %+v, %v", got, err)
	}
}

func TestRotationCancellationRecordsFailure(t *testing.T) {
	s, _ := newService(t, newClock(time.Now()))

	entered := make(chan struct{}, 1)
	dns := &memoryDNS{}
	s.configure(t, func(c *Config, d *Dependencies) {
		c.DNSDomain = "vpn.example.com"
		d.DNS = dns
		d.Checker = probe.CheckerFunc(func(ctx context.Context, _ probe.Request) error {
			entered <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		})
	})
	ctx := context.Background()
	n, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{Name: "n", Host: "192.0.2.1", IP: "192.0.2.1", VLESS: &panel.VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Network.EnableDNS(ctx, n.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Network.AddAddress(ctx, n.ID, "192.0.2.2"); err != nil {
		t.Fatal(err)
	}
	op, err := s.Network.RotateIP(ctx, n.ID, panel.RotateIPInput{Mode: "existing_ip", IP: "192.0.2.2"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("rotation never checked candidate")
	}
	// Application shutdown cancels the running rotation and waits for it to record the outcome.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, err := s.Store.Network.GetNetworkOperation(ctx, op.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "running" {
			if got.Status != "failed" || got.Error != "cancelled" || got.FinishedAt == nil {
				t.Fatalf("cancelled operation: %+v", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancellation not persisted")
		}
		time.Sleep(time.Millisecond)
	}
	got, err := s.Store.Nodes.GetNode(ctx, n.ID)
	if err != nil || got.IP != "192.0.2.1" {
		t.Fatalf("cancelled rotation changed active IP: %+v, %v", got, err)
	}
}

func TestSafeErrorRepresentation(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{{nil, ""}, {context.Canceled, "cancelled"}, {context.DeadlineExceeded, "timeout"}, {errors.New("first\nsecond"), "first second"}} {
		if got := operation.SafeError(tc.err); got != tc.want {
			t.Fatalf("error representation: %q, want %q", got, tc.want)
		}
	}
}
