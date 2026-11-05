package agent

import (
	"context"
	"errors"

	"github.com/artemlavr/giocondo/internal/platform/metrics"
	"github.com/artemlavr/giocondo/internal/worker/panelsync"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// syncer builds the Sync worker once, together with the engines and panel connections it drives.
func (a *App) syncer() (*panelsync.Syncer, error) {
	if a.panelSync != nil {
		return a.panelSync, nil
	}

	// Checked here because the engine monitor tickers start before the syncer validates itself.
	if a.cfg.StatsInterval <= 0 || a.cfg.HealthInterval <= 0 {
		return nil, errors.New("stats and health intervals must be positive")
	}

	engines, err := a.engineManager()
	if err != nil {
		return nil, err
	}

	panels, err := a.panelConns()
	if err != nil {
		return nil, err
	}

	a.panelSync = &panelsync.Syncer{
		Panels:         panels,
		Engines:        engines,
		Traffic:        a.traffic,
		Network:        a.network,
		ReadHost:       a.readHost,
		Version:        a.version,
		StatsInterval:  a.cfg.StatsInterval,
		HealthInterval: a.cfg.HealthInterval,
	}

	return a.panelSync, nil
}

// recoverEngines restores every engine from its journal before any apply can run. A blocked engine
// is not fatal: it reports its state to the panel and refuses applies, while the agent keeps
// syncing and the other engines keep working.
func (a *App) recoverEngines(ctx context.Context) {
	if err := a.engines.Open(ctx); err != nil {
		logger.FromCtx(ctx).Error("engine recovery blocked", "err", err)
	}
}

// startWorkers starts the engine queues and monitors and the network worker. The returned function
// waits for all of them once ctx is cancelled.
func (a *App) startWorkers(ctx context.Context) (wait func()) {
	a.engines.Start(ctx)
	a.engines.Monitor(ctx, a.cfg.StatsInterval, a.cfg.HealthInterval)

	networkDone := make(chan struct{})
	go func() { defer close(networkDone); a.network.Run(ctx) }()

	return func() {
		<-networkDone
		a.engines.Wait()
	}
}

func readHostMetrics() panelsync.HostMetrics {
	m := metrics.Reader{}.Read()

	return panelsync.HostMetrics{
		Load1:         m.Load1,
		MemTotalBytes: m.MemTotalBytes, MemAvailableBytes: m.MemAvailableBytes, AgentRSSBytes: m.AgentRSSBytes,
	}
}
