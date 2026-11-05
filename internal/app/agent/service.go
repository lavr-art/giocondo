package agent

import (
	"fmt"
	"path/filepath"

	"github.com/artemlavr/giocondo/internal/platform/singboxctl"
	"github.com/artemlavr/giocondo/internal/repository/enginefs"
	"github.com/artemlavr/giocondo/internal/service/engine"
	"github.com/artemlavr/giocondo/internal/transport/grpc/v2raystats"
	"github.com/artemlavr/giocondo/internal/worker/enginequeue"
)

// engineManager locks the state directory for this process and builds the sing-box runtime once.
func (a *App) engineManager() (*enginequeue.Manager, error) {
	if a.engines != nil {
		return a.engines, nil
	}

	stats, err := a.statsClient()
	if err != nil {
		return nil, err
	}

	lock, err := enginefs.Lock(a.cfg.StateDir)
	if err != nil {
		return nil, fmt.Errorf("lock agent state: %w", err)
	}

	a.closers = append(a.closers, lock.Close)

	engines, err := enginequeue.NewManager(singBoxRuntime(a.cfg, singBoxDriver(a.cfg, stats), a.traffic.Add))
	if err != nil {
		return nil, err
	}

	a.engines = engines

	return a.engines, nil
}

// singBoxDriver builds the sing-box driver from cfg. A nil stats client disables traffic collection.
func singBoxDriver(cfg Config, stats v2raystats.StatsServiceClient) *singboxctl.Driver {
	return &singboxctl.Driver{
		Binary: cfg.SingBoxBin, Stats: stats,
		RestartCmd: cfg.RestartCmd, StatusCmd: cfg.StatusCmd, StopCmd: cfg.StopCmd,
	}
}

// singBoxRuntime builds the sing-box runtime from cfg. Each engine keeps its journal in its own
// subdirectory of the agent state directory, and onTraffic receives the counters read before restarts
// and on each collection.
func singBoxRuntime(cfg Config, driver *singboxctl.Driver, onTraffic func(map[string]engine.Traffic)) *engine.Runtime {
	return engine.New(
		driver,
		enginefs.NewStore(),
		cfg.ConfigPath,
		filepath.Join(cfg.StateDir, driver.ID()),
		cfg.SettleDelay,
		onTraffic,
	)
}
