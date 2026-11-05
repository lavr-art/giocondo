package panelapp

import (
	"time"

	"github.com/artemlavr/giocondo/internal/transport/grpc/nodehub"
	"github.com/artemlavr/giocondo/internal/worker/healthsched"
	"github.com/artemlavr/giocondo/internal/worker/reconciliation"
	"github.com/artemlavr/giocondo/internal/worker/tasks"
	"github.com/artemlavr/giocondo/internal/worker/watchsched"
)

// TaskExecutor returns the task executor.
func (a *App) TaskExecutor() *tasks.Executor {
	a.executorOnce.Do(func() { a.executor = tasks.New(a.background) })

	return a.executor
}

func (a *App) startWorkers(start func(func())) {
	if a.hooks != nil {
		start(func() { a.hooks.Run(a.background) })
	}

	start(func() {
		(&watchsched.Worker{
			Service:  a.WatcherService(),
			Interval: time.Minute,
		}).Run(a.background)
	})
	start(func() {
		(&healthsched.Worker{
			Service:  a.HealthcheckService(),
			Interval: a.HealthcheckService().Interval(),
		}).Run(a.background)
	})
	start(func() { (&reconciliation.Worker{Source: a.hub, Interval: nodehub.DefaultResync}).Run(a.background) })
}
