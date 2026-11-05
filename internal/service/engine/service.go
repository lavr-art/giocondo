// Package engine manages external VPN services without importing their cores.
package engine

import (
	"context"
	"sync"
	"time"
)

// Driver owns engine-specific commands. Runtime owns persistence and ordering.
// All methods must honor context cancellation and must not log configuration secrets.
type Driver interface {
	ID() string
	Version(context.Context) (string, error)
	Validate(context.Context, string) error
	Restart(context.Context) error
	Stop(context.Context) error
	Active(context.Context) (bool, error)
	Collect(context.Context) (map[string]Traffic, error)
}

// Store supplies the durable file operations required by Runtime.
// ReadFile must report absent files with an error matching fs.ErrNotExist.
// Writes, directory creation, and removal must be durable before returning.
type Store interface {
	ReadFile(string) ([]byte, error)
	AtomicWrite(string, []byte) error
	MkdirDurable(string) error
	RemoveFile(string) error
}

// Runtime owns the managed config file, the apply journal and the service lifecycle of one engine.
// Apply, Open, Collect and the restarts they cause are serialized, so counters are always read
// before a restart resets them. Snapshot never waits for external calls, so a slow engine cannot
// stall Sync.
type Runtime struct {
	driver     Driver
	store      Store
	configPath string
	stateDir   string
	// settleDelay is the wait after a restart before checking that the engine is still active.
	settleDelay time.Duration
	// onTraffic receives counters read before each restart and on Collect; nil drops them.
	onTraffic func(map[string]Traffic)

	op    sync.Mutex   // serializes operations that touch files or the service
	mu    sync.RWMutex // guards state
	state State
	j     journal // last persisted journal; accessed under op
	// checkpoint is used by subprocess tests to interrupt actual durable phases.
	checkpoint func(string)
}

// New returns the runtime of the engine driven by driver. It manages the config at configPath and keeps
// its journal in stateDir; settleDelay is the wait after a restart before checking that the engine is
// still active. onTraffic receives counters read before each restart and on Collect; nil drops them.
func New(
	driver Driver,
	store Store,
	configPath string,
	stateDir string,
	settleDelay time.Duration,
	onTraffic func(map[string]Traffic),
) *Runtime {
	return &Runtime{
		driver:      driver,
		store:       store,
		configPath:  configPath,
		stateDir:    stateDir,
		settleDelay: settleDelay,
		onTraffic:   onTraffic,
	}
}

// ID returns the driver ID, or an empty string without a driver.
func (r *Runtime) ID() string {
	if r.driver == nil {
		return ""
	}

	return r.driver.ID()
}
