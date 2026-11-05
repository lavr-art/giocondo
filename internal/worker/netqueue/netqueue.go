// Package netqueue applies host network commands one at a time, independently of Sync sessions.
package netqueue

import (
	"context"
	"sync"
	"time"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
	"github.com/artemlavr/giocondo/pkg/logger"
)

const applyTimeout = 10 * time.Second

// Worker applies the newest network command. At most one command runs and one waits; a newer
// command replaces the waiting one, because each command describes the complete desired state.
// Submit and Results are safe for concurrent use; Run is started once by the owner.
type Worker struct {
	apply   func(context.Context, *nodesync.NetworkConfig) error
	results chan *nodesync.ApplyResult
	wake    chan struct{}

	mu      sync.Mutex
	pending *nodesync.NetworkConfig
}

// New creates a worker that applies commands with apply.
func New(apply func(context.Context, *nodesync.NetworkConfig) error) *Worker {
	return &Worker{
		apply:   apply,
		results: make(chan *nodesync.ApplyResult, 1),
		wake:    make(chan struct{}, 1),
	}
}

// Submit queues cfg without blocking and returns the waiting command it replaced, if any.
// The replaced command is never applied and gets no result from the worker.
func (w *Worker) Submit(cfg *nodesync.NetworkConfig) (superseded *nodesync.NetworkConfig) {
	w.mu.Lock()
	superseded, w.pending = w.pending, cfg
	w.mu.Unlock()

	select {
	case w.wake <- struct{}{}:
	default:
	}

	return superseded
}

// Results delivers the outcome of each applied command. The worker waits for a reader before it
// applies the next command, so results are not lost while Sync reconnects.
func (w *Worker) Results() <-chan *nodesync.ApplyResult { return w.results }

// Run applies queued commands until ctx is cancelled. It returns only after the active application
// has finished, which is bounded by applyTimeout.
func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		}

		w.mu.Lock()
		cfg := w.pending
		w.pending = nil
		w.mu.Unlock()

		if cfg == nil {
			continue
		}

		select {
		case w.results <- w.run(ctx, cfg):
		case <-ctx.Done():
			return
		}
	}
}

func (w *Worker) run(ctx context.Context, cfg *nodesync.NetworkConfig) *nodesync.ApplyResult {
	c, cancel := context.WithTimeout(ctx, applyTimeout)
	defer cancel()

	if err := w.apply(c, cfg); err != nil {
		// The error can name host addresses and firewall output; the panel gets a generic message.
		logger.FromCtx(ctx).Error("apply UDP aliases failed")

		return &nodesync.ApplyResult{Hash: cfg.Hash, Error: "network configuration failed"}
	}

	return &nodesync.ApplyResult{Hash: cfg.Hash, Ok: true}
}
