package panelsync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
	"github.com/artemlavr/giocondo/internal/service/engine"
	"github.com/artemlavr/giocondo/internal/worker/enginequeue"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// session is the state of one Sync stream. A single goroutine, the loop in run, sends every message,
// so external operations never block the stream: applies report back through applied.
type session struct {
	syncer *Syncer
	stream nodesync.Stream
	inst   instance

	// awaiting maps each hash this session submitted to the future whose outcome it will report.
	// Only the loop goroutine accesses it.
	awaiting map[string]*enginequeue.Future
	applied  chan applyOutcome
	waiters  sync.WaitGroup
}

type applyOutcome struct {
	hash   string
	future *enginequeue.Future
}

func (s *Syncer) session(ctx context.Context, p Panel, inst instance) error {
	sctx, cancel := context.WithCancel(ctx)

	sess := &session{
		syncer: s, inst: inst,
		awaiting: map[string]*enginequeue.Future{}, applied: make(chan applyOutcome),
	}
	// Waiters stop on sctx; an apply they waited for keeps running in its engine queue.
	defer func() { cancel(); sess.waiters.Wait() }()

	stream, err := p.Conn.Sync(sctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

	sess.stream = stream

	return sess.run(ctx, sctx)
}

func (s *session) run(ctx, sctx context.Context) error {
	hello := s.hello()
	if err := s.send(&nodesync.AgentMessage{Hello: hello}); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}

	logger.FromCtx(ctx).Info("connected to panel", "config_hash", short(hello.ConfigHash))

	recv, recvErr := receive(sctx, s.stream)

	statsT := time.NewTicker(s.syncer.StatsInterval)
	defer statsT.Stop()

	healthT := time.NewTicker(s.syncer.HealthInterval)
	defer healthT.Stop()

	if err := s.send(s.health()); err != nil {
		return err
	}
	// A batch left unacknowledged in the previous session is sent right away.
	if err := s.sendStats(); err != nil {
		return err
	}

	for {
		var err error

		select {
		case <-ctx.Done():
			_ = s.stream.CloseSend()

			return ctx.Err()
		case err = <-recvErr:
			return err
		case m := <-recv:
			err = s.handle(sctx, m)
		case o := <-s.applied:
			err = s.reportApply(o)
		case result := <-s.syncer.Network.Results():
			err = s.send(&nodesync.AgentMessage{NetworkResult: result})
		case <-statsT.C:
			err = s.sendStats()
		case <-healthT.C:
			err = s.send(s.health())
		}

		if err != nil {
			return err
		}
	}
}

// receive forwards stream messages until the stream fails or ctx ends.
func receive(ctx context.Context, stream nodesync.Stream) (<-chan *nodesync.PanelMessage, <-chan error) {
	recv := make(chan *nodesync.PanelMessage)
	recvErr := make(chan error, 1)

	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				recvErr <- err

				return
			}

			select {
			case recv <- m:
			case <-ctx.Done():
				return
			}
		}
	}()

	return recv, recvErr
}

func (s *session) send(m *nodesync.AgentMessage) error { return s.stream.Send(m) }

func (s *session) sendStats() error {
	if st := s.syncer.Traffic.next(); st != nil {
		return s.send(&nodesync.AgentMessage{Stats: st})
	}

	return nil
}

func (s *session) handle(ctx context.Context, m *nodesync.PanelMessage) error {
	switch {
	case m.ApplyConfig != nil:
		return s.submitConfig(ctx, m.ApplyConfig)
	case m.NetworkConfig != nil:
		return s.submitNetwork(m.NetworkConfig)
	case m.StatsAck != nil:
		s.syncer.Traffic.ack(m.StatsAck.Seq)

		return nil
	default:
		return errors.New("unknown message from panel")
	}
}

// submitConfig queues the config on its engine and waits for the outcome in the background.
// Submit is called even for a hash already awaited: resubmitting the running hash supersedes a
// pending one in the engine queue.
func (s *session) submitConfig(ctx context.Context, cfg *nodesync.ApplyConfig) error {
	if engine.Hash(cfg.ConfigJSON) != cfg.Hash {
		mismatch := &nodesync.ApplyResult{Hash: cfg.Hash, Error: "config hash mismatch"}

		return s.send(&nodesync.AgentMessage{ApplyResult: mismatch})
	}

	f := s.syncer.Engines.Submit(nodesync.SingBoxEngineID, cfg.ConfigJSON, cfg.Hash)
	if s.awaiting[cfg.Hash] == f {
		return nil
	}

	s.awaiting[cfg.Hash] = f
	s.waiters.Go(func() {
		select {
		case <-f.Done():
		case <-ctx.Done():
			return
		}

		select {
		case s.applied <- applyOutcome{hash: cfg.Hash, future: f}:
		case <-ctx.Done():
		}
	})

	return nil
}

// reportApply sends the outcome of an awaited apply, followed by fresh health. An outcome of a future
// that a later submission of the same hash replaced is dropped; the replacing one is reported.
func (s *session) reportApply(o applyOutcome) error {
	if s.awaiting[o.hash] != o.future {
		return nil
	}

	delete(s.awaiting, o.hash)

	res := &nodesync.ApplyResult{Hash: o.hash, Ok: true}
	if err := o.future.Err(); err != nil {
		res.Ok, res.Error = false, err.Error()
	}

	if err := s.send(&nodesync.AgentMessage{ApplyResult: res}); err != nil {
		return err
	}

	return s.send(s.health())
}

// submitNetwork queues the network command; a command it replaced before being applied is reported
// as superseded, so every command the panel sent gets exactly one result.
func (s *session) submitNetwork(cfg *nodesync.NetworkConfig) error {
	old := s.syncer.Network.Submit(cfg)
	if old == nil {
		return nil
	}

	superseded := &nodesync.ApplyResult{Hash: old.Hash, Error: nodesync.ErrNetworkConfigSuperseded.Error()}

	return s.send(&nodesync.AgentMessage{NetworkResult: superseded})
}
