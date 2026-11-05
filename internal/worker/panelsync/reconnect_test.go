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
	"github.com/artemlavr/giocondo/internal/worker/netqueue"
)

// streamConnector hands every new session's stream to the test.
type streamConnector struct{ streams chan *chanStream }

func (c streamConnector) Sync(ctx context.Context) (nodesync.Stream, error) {
	s := &chanStream{ctx: ctx, in: make(chan *nodesync.PanelMessage), sent: make(chan *nodesync.AgentMessage, 16), fail: make(chan error, 1)}
	c.streams <- s

	return s, nil
}

// helloAndStats reads one session's messages until both Hello and the first Stats batch arrived.
func helloAndStats(t *testing.T, s *chanStream) (*nodesync.Hello, *nodesync.Stats) {
	t.Helper()

	var hello *nodesync.Hello
	timeout := time.After(3 * time.Second)
	for {
		select {
		case m := <-s.sent:
			if m.Hello != nil {
				hello = m.Hello
			}
			if m.Stats != nil {
				if hello == nil {
					t.Fatal("stats sent before hello")
				}
				return hello, m.Stats
			}
		case <-timeout:
			t.Fatal("session did not send hello and stats")
		}
	}
}

// The panel deduplicates traffic by instance ID and seq, so a batch resent after a reconnect must
// carry both unchanged; otherwise the same bytes are counted twice.
func TestReconnectResendsUnackedBatchUnderSameInstance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}

	m, err := enginequeue.NewManager(engine.New(
		&blockingDriver{start: make(chan struct{}), release: make(chan struct{})},
		enginefs.NewStore(), config, filepath.Join(dir, "state"), 0, nil,
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Open(ctx); err != nil {
		t.Fatal(err)
	}

	traffic := &TrafficBuffer{}
	traffic.Add(map[string]engine.Traffic{"alice": {Up: 5}})

	conn := streamConnector{streams: make(chan *chanStream, 2)}
	s := &Syncer{
		Panels: []Panel{{Addr: "test", Conn: conn}}, Engines: m, Traffic: traffic, Network: netqueue.New(nil),
		StatsInterval: time.Hour, HealthInterval: time.Hour,
	}

	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	first := <-conn.streams
	hello1, stats1 := helloAndStats(t, first)
	first.fail <- errors.New("connection dropped before ack")

	second := <-conn.streams
	hello2, stats2 := helloAndStats(t, second)

	if hello1.InstanceID == "" || hello2.InstanceID != hello1.InstanceID {
		t.Fatalf("instance %q after reconnect, want %q", hello2.InstanceID, hello1.InstanceID)
	}
	if stats2.Seq != stats1.Seq || len(stats2.Users) != 1 || stats2.Users[0].Up != 5 {
		t.Fatalf("resent batch %+v, want seq %d with alice up 5", stats2, stats1.Seq)
	}

	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run: %v", err)
	}
}
