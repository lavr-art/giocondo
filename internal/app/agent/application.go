// Package agent wires the node agent application.
package agent

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/artemlavr/giocondo/internal/platform/hostnet"
	"github.com/artemlavr/giocondo/internal/transport/grpc/v2raystats"
	"github.com/artemlavr/giocondo/internal/worker/enginequeue"
	"github.com/artemlavr/giocondo/internal/worker/netqueue"
	"github.com/artemlavr/giocondo/internal/worker/panelsync"
)

// App owns the agent lifecycle. Dependencies live in private fields; their constructor methods create
// and cache them, and Run prepares all of them before starting concurrent work.
type App struct {
	cfg      Config
	version  string
	readHost func() panelsync.HostMetrics

	// traffic is built first: the engines fill it and the syncer drains it.
	traffic   *panelsync.TrafficBuffer
	panels    []panelsync.Panel // primary address and fallbacks, in order of preference
	stats     v2raystats.StatsServiceClient
	engines   *enginequeue.Manager
	panelSync *panelsync.Syncer

	network *netqueue.Worker // no dependencies to build; Run starts it with the other workers

	background      context.Context //nolint:containedctx // Lifecycle root: Close cancels it, then joins workers.
	cancel          context.CancelFunc
	mu              sync.Mutex
	running, closed bool
	runDone         chan struct{}
	closers         []func() error
	closeOnce       sync.Once
	closeErr        error
}

// New creates the agent without opening anything: Run builds the dependencies from cfg
// and owns every resource it opens until Close.
func New(cfg Config, version string) *App {
	background, cancel := context.WithCancel(context.Background())

	return &App{
		cfg:        cfg,
		version:    version,
		traffic:    &panelsync.TrafficBuffer{},
		readHost:   readHostMetrics,
		background: background,
		cancel:     cancel,
		network:    netqueue.New(hostnet.ApplyNetwork),
	}
}

// Run builds the dependencies, recovers the engines, starts the workers and holds the Sync stream
// until ctx is cancelled or Close is called. It returns after every worker has stopped and the
// resources are released. Run can be called once; a second call fails without affecting the first.
func (a *App) Run(ctx context.Context) (err error) {
	a.mu.Lock()
	if a.closed || a.running {
		a.mu.Unlock()

		return errors.New("agent already running or closed")
	}

	a.running = true
	a.runDone = make(chan struct{})
	done := a.runDone
	a.mu.Unlock()

	defer func() {
		close(done)

		err = errors.Join(err, a.Close())
	}()

	work, cancel := context.WithCancel(ctx)

	// Close cancels the application context; the workers follow it as well as the caller context.
	stop := context.AfterFunc(a.background, cancel) //nolint:contextcheck // Close must stop the caller-derived work.
	defer stop()

	// Dependencies are built before any worker starts; a failure releases what was opened via Close.
	syncer, err := a.syncer()
	if err != nil {
		cancel()

		return err
	}

	a.recoverEngines(work)

	wait := a.startWorkers(work)
	// Resources are released only after the network and engine workers have stopped.
	defer func() { cancel(); wait() }()

	return syncer.Run(work)
}

// Close stops a running Run, waits for it and releases every resource it opened, in reverse order.
// It is safe to call repeatedly and concurrently; later calls return the first result.
func (a *App) Close() error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.cancel()
		done := a.runDone
		a.mu.Unlock()

		if done != nil {
			<-done
		}

		for _, v := range slices.Backward(a.closers) {
			a.closeErr = errors.Join(a.closeErr, v())
		}
	})

	return a.closeErr
}
