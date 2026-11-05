// Package healthsched schedules node availability checks.
package healthsched

import (
	"context"
	"sync"
	"time"

	"github.com/artemlavr/giocondo/pkg/logger"
)

// Service checks all eligible nodes and waits for the probes it starts.
type Service interface{ CheckNodesOnce(context.Context) error }

// Worker runs the node availability checks periodically. Interval must be positive.
type Worker struct {
	Service  Service
	Interval time.Duration
}

// Run checks immediately and then periodically until cancellation.
func (w *Worker) Run(ctx context.Context) {
	timer := time.NewTicker(w.Interval)
	defer timer.Stop()

	for {
		if err := w.Service.CheckNodesOnce(ctx); err != nil && ctx.Err() == nil {
			logger.FromCtx(ctx).Error("protocol health check", "err", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

// RunProbes executes at most four probes at a time and waits for every admitted
// probe, including when cancellation interrupts admission of the remaining work.
func RunProbes(ctx context.Context, probes []func(context.Context)) error {
	sem := make(chan struct{}, 4)

	var wg sync.WaitGroup
	defer wg.Wait()

	for _, probe := range probes {
		if err := ctx.Err(); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case sem <- struct{}{}:
		}

		if err := ctx.Err(); err != nil {
			<-sem

			return err
		}

		wg.Go(func() {
			defer func() { <-sem }()

			probe(ctx)
		})
	}

	wg.Wait()

	return ctx.Err()
}
