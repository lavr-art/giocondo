package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Future is shared by duplicate submissions and completes exactly once.
type Future struct {
	done chan struct{}
	err  error
}

func (f *Future) Done() <-chan struct{} { return f.done }
func (f *Future) Err() error            { <-f.done; return f.err }
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
	runtime          *Runtime
	mu               sync.Mutex
	current, pending *request
	wake             chan struct{}
	closed           bool
}
type Manager struct {
	workers map[string]*worker
	wg      sync.WaitGroup
}

func NewManager(runtimes ...*Runtime) (*Manager, error) {
	m := &Manager{workers: map[string]*worker{}}
	for _, r := range runtimes {
		if r == nil || r.Driver == nil {
			return nil, errors.New("nil engine driver")
		}
		id := r.Driver.ID()
		if id == "" || m.workers[id] != nil {
			return nil, fmt.Errorf("invalid or duplicate engine ID %q", id)
		}
		m.workers[id] = &worker{runtime: r, wake: make(chan struct{}, 1)}
	}
	return m, nil
}
func (m *Manager) Runtime(id string) *Runtime {
	if w := m.workers[id]; w != nil {
		return w.runtime
	}
	return nil
}
func (m *Manager) States() []State {
	out := make([]State, 0, len(m.workers))
	for _, w := range m.workers {
		state := w.runtime.Snapshot()
		state.ID = w.runtime.Driver.ID()
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
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
			w.pending.future.finish(errors.New("configuration superseded"))
			w.pending = nil
		}
		return w.current.future
	}
	if w.pending != nil && w.pending.hash == hash {
		return w.pending.future
	}
	if w.pending != nil {
		w.pending.future.finish(errors.New("configuration superseded"))
	}
	w.pending = &request{data: append([]byte(nil), data...), hash: hash, future: f}
	select {
	case w.wake <- struct{}{}:
	default:
	}
	return f
}
func (m *Manager) Start(ctx context.Context) {
	for _, w := range m.workers {
		m.wg.Add(1)
		go func() { defer m.wg.Done(); w.run(ctx) }()
	}
}
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
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
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
		}()
	}
}
