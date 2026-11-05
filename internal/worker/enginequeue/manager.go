// Package enginequeue runs one apply queue and one monitor loop per engine runtime: a single current
// apply, one replaceable pending config, and periodic counter collection and activity checks.
package enginequeue

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
	"github.com/artemlavr/giocondo/internal/service/engine"
)

// Future is shared by duplicate submissions and completes exactly once.
type Future struct {
	done chan struct{}
	err  error
}

// Done is closed when the apply finished, was superseded or was cancelled.
func (f *Future) Done() <-chan struct{} { return f.done }

// Err waits for Done and returns the apply error; nil means the config is applied.
func (f *Future) Err() error {
	<-f.done

	return f.err
}

// Wait is Err bounded by ctx.
func (f *Future) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		return f.err
	}
}

func (f *Future) finish(e error) { f.err = e; close(f.done) }

type request struct {
	data   []byte
	hash   string
	future *Future
}

type worker struct {
	runtime          *engine.Runtime
	mu               sync.Mutex
	current, pending *request
	wake             chan struct{}
	closed           bool
}

// Manager owns one apply worker per engine runtime. Each worker runs one apply at a time and keeps
// one replaceable pending config, so a burst of configs applies only the first and the newest.
type Manager struct {
	workers map[string]*worker
	wg      sync.WaitGroup
}

// NewManager creates a manager for runtimes with distinct, non-empty driver IDs.
func NewManager(runtimes ...*engine.Runtime) (*Manager, error) {
	m := &Manager{workers: map[string]*worker{}}

	for _, r := range runtimes {
		if r == nil {
			return nil, errors.New("nil engine runtime")
		}

		id := r.ID()
		if id == "" || m.workers[id] != nil {
			return nil, fmt.Errorf("invalid or duplicate engine ID %q", id)
		}

		m.workers[id] = &worker{runtime: r, wake: make(chan struct{}, 1)}
	}

	return m, nil
}

// Open recovers every runtime from its journal; it must complete before Start and Monitor.
// A runtime that fails to recover stays blocked and keeps reporting its state, so the error lists
// the affected engines without preventing the others from working.
func (m *Manager) Open(ctx context.Context) error {
	var errs []error

	for id, w := range m.workers {
		if err := w.runtime.Open(ctx); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
	}

	return errors.Join(errs...)
}

// States returns a snapshot of every engine, sorted by ID. It never waits for a running apply.
func (m *Manager) States() []engine.State {
	out := make([]engine.State, 0, len(m.workers))
	for _, w := range m.workers {
		state := w.runtime.Snapshot()
		state.ID = w.runtime.ID()
		out = append(out, state)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out
}

// Submit queues data, whose hash is hash, on the engine id and returns its future without blocking.
// Submitting the running hash again shares its future and supersedes the pending config; submitting the
// pending hash shares the pending future; any other hash supersedes the pending config.
func (m *Manager) Submit(id string, data []byte, hash string) *Future {
	f := &Future{done: make(chan struct{})}

	w := m.workers[id]
	if w == nil {
		f.finish(errors.New("unknown engine"))

		return f
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		f.finish(context.Canceled)

		return f
	}

	if w.current != nil && w.current.hash == hash {
		if w.pending != nil {
			w.pending.future.finish(nodesync.ErrConfigSuperseded)
			w.pending = nil
		}

		return w.current.future
	}

	if w.pending != nil && w.pending.hash == hash {
		return w.pending.future
	}

	if w.pending != nil {
		w.pending.future.finish(nodesync.ErrConfigSuperseded)
	}

	w.pending = &request{data: append([]byte(nil), data...), hash: hash, future: f}
	select {
	case w.wake <- struct{}{}:
	default:
	}

	return f
}

// Start starts the apply workers; they stop when ctx is cancelled and fail their pending configs.
func (m *Manager) Start(ctx context.Context) {
	for _, w := range m.workers {
		m.wg.Go(func() { w.run(ctx) })
	}
}

// Wait blocks until every goroutine started by Start and Monitor has returned.
func (m *Manager) Wait() { m.wg.Wait() }

func (w *worker) run(ctx context.Context) {
	defer func() {
		w.mu.Lock()
		defer w.mu.Unlock()

		w.closed = true
		if w.pending != nil {
			w.pending.future.finish(context.Canceled)
			w.pending = nil
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		}

		for {
			if ctx.Err() != nil {
				return
			}

			w.mu.Lock()
			req := w.pending
			w.pending = nil
			w.current = req
			w.mu.Unlock()

			if req == nil {
				break
			}

			e := w.runtime.Apply(ctx, req.data, req.hash)
			w.mu.Lock()
			req.future.finish(e)

			w.current = nil
			w.mu.Unlock()
		}
	}
}

// Monitor runs independent collection and health loops for each engine.
func (m *Manager) Monitor(ctx context.Context, statsEvery, healthEvery time.Duration) {
	for _, w := range m.workers {
		m.wg.Go(func() {
			stats := time.NewTicker(statsEvery)
			defer stats.Stop()

			health := time.NewTicker(healthEvery)
			defer health.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-stats.C:
					w.runtime.Collect(ctx)
				case <-health.C:
					w.runtime.Refresh(ctx)
				}
			}
		})
	}
}
