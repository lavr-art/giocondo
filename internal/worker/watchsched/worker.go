// Package watchsched schedules user status checks and notifications.
package watchsched

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/pkg/logger"
)

// Service performs one user status and notification pass.
type Service interface{ WatchOnce(context.Context) error }

// Worker runs the user watch pass periodically.
type Worker struct {
	Service  Service
	Interval time.Duration
}

// Run checks immediately and then every Interval until cancellation.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.Interval)
	defer t.Stop()

	for {
		if err := w.Service.WatchOnce(ctx); err != nil && ctx.Err() == nil {
			logger.FromCtx(ctx).Error("watch", "err", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
