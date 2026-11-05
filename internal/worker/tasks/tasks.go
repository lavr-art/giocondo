// Package tasks owns cancellable background tasks and waits for their cleanup.
package tasks

import (
	"context"
	"sync"
	"time"
)

// Executor runs tasks.
type Executor struct {
	ctx    context.Context //nolint:containedctx // Task parent: Close cancels it under mu, then waits.
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// New returns an executor whose tasks are cancelled when ctx ends or Close is called.
func New(ctx context.Context) *Executor {
	work, cancel := context.WithCancel(ctx)

	return &Executor{ctx: work, cancel: cancel}
}

// Submit admits work atomically with Close. Tasks outlive requests but not the app.
func (e *Executor) Submit(timeout time.Duration, fn func(context.Context)) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed || e.ctx.Err() != nil {
		return false
	}

	e.wg.Go(func() {
		ctx, cancel := context.WithTimeout(e.ctx, timeout)
		defer cancel()

		fn(ctx)
	})

	return true
}

// Close rejects new tasks, cancels running ones and waits for them to return.
func (e *Executor) Close() {
	e.mu.Lock()
	e.closed = true
	e.cancel()
	e.mu.Unlock()

	e.wg.Wait()
}
