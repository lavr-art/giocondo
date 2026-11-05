package panelsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
	"github.com/artemlavr/giocondo/internal/repository/enginefs"
	"github.com/artemlavr/giocondo/internal/service/engine"
	"github.com/artemlavr/giocondo/internal/worker/enginequeue"
)

// chanStream is an in-memory Sync stream: the test feeds panel messages and reads agent messages.
type chanStream struct {
	ctx  context.Context
	in   chan *nodesync.PanelMessage
	sent chan *nodesync.AgentMessage
	fail chan error // a value ends the stream as a dropped connection would
}

func (s *chanStream) Send(m *nodesync.AgentMessage) error {
	select {
	case s.sent <- m:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *chanStream) Recv() (*nodesync.PanelMessage, error) {
	select {
	case m := <-s.in:
		return m, nil
	case err := <-s.fail:
		return nil, err
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	}
}

func (*chanStream) CloseSend() error { return nil }

type chanConnector struct {
	in   chan *nodesync.PanelMessage
	sent chan *nodesync.AgentMessage
}

func (c chanConnector) Sync(ctx context.Context) (nodesync.Stream, error) {
	return &chanStream{ctx: ctx, in: c.in, sent: c.sent}, nil
}

// nextNetworkResult skips other agent messages until a network result arrives.
func nextNetworkResult(t *testing.T, sent <-chan *nodesync.AgentMessage) *nodesync.ApplyResult {
	t.Helper()

	timeout := time.After(2 * time.Second)
	for {
		select {
		case m := <-sent:
			if m.NetworkResult != nil {
				return m.NetworkResult
			}
		case <-timeout:
			t.Fatal("no network result")
		}
	}
}

func TestNewerNetworkCommandSupersedesQueuedOne(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}

	traffic := &TrafficBuffer{}
	d := &blockingDriver{start: make(chan struct{}), release: make(chan struct{})}
	r := engine.New(d, enginefs.NewStore(), config, filepath.Join(dir, "state"), 0, traffic.Add)
	m, err := enginequeue.NewManager(r)
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var applied []string
	applyNetwork := func(ctx context.Context, cfg *nodesync.NetworkConfig) error {
		applied = append(applied, cfg.Hash)
		started <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	conn := chanConnector{in: make(chan *nodesync.PanelMessage), sent: make(chan *nodesync.AgentMessage, 16)}
	s := &Syncer{
		Panels: []Panel{{Addr: "test", Conn: conn}}, Engines: m, Traffic: traffic,
		StatsInterval: time.Minute, HealthInterval: time.Minute,
	}

	done := make(chan error, 1)
	go func() { done <- runSyncer(ctx, s, applyNetwork) }()

	network := func(hash string) {
		select {
		case conn.in <- &nodesync.PanelMessage{NetworkConfig: &nodesync.NetworkConfig{PrimaryIP: "192.0.2.1", Hash: hash}}:
		case <-ctx.Done():
			t.Fatal("agent did not receive network command")
		}
	}

	network("one")
	<-started
	network("two")
	network("three")

	if got := nextNetworkResult(t, conn.sent); got.Hash != "two" || got.Ok || got.Error != "network configuration superseded" {
		t.Fatalf("first result %+v, want the queued command superseded", got)
	}

	close(release)

	for _, want := range []string{"one", "three"} {
		if got := nextNetworkResult(t, conn.sent); got.Hash != want || !got.Ok {
			t.Fatalf("result %+v, want %s applied", got, want)
		}
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}

	if len(applied) != 2 || applied[0] != "one" || applied[1] != "three" {
		t.Fatalf("applied %v, want [one three]", applied)
	}
}
