// Package reconciliation schedules desired node configuration updates.
package reconciliation

import (
	"context"
	"time"
)

// Source provides one reconciliation pass and a subscription to config changes.
type Source interface {
	ReconcileOnce(context.Context)
	SubscribeReconciliation() (<-chan struct{}, func())
}

// Worker runs background reconciliation.
type Worker struct {
	Source   Source
	Interval time.Duration
}

// Run reconciles immediately, periodically and when the source signals a change.
func (w *Worker) Run(ctx context.Context) {
	kick, unsubscribe := w.Source.SubscribeReconciliation()
	defer unsubscribe()

	t := time.NewTicker(w.Interval)
	defer t.Stop()

	for {
		w.Source.ReconcileOnce(ctx)

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-kick:
		}
	}
}
