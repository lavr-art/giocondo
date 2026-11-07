package panelapp

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"sync"
	"time"

	"google.golang.org/grpc"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
	"github.com/artemlavr/giocondo/internal/service/healthcheck"
	"github.com/artemlavr/giocondo/internal/service/hosts"
	"github.com/artemlavr/giocondo/internal/service/network"
	"github.com/artemlavr/giocondo/internal/service/nodeconfig"
	"github.com/artemlavr/giocondo/internal/service/nodeprobe"
	"github.com/artemlavr/giocondo/internal/service/nodes"
	"github.com/artemlavr/giocondo/internal/service/nodestate"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/internal/service/provisioning"
	"github.com/artemlavr/giocondo/internal/service/settings"
	"github.com/artemlavr/giocondo/internal/service/tokens"
	"github.com/artemlavr/giocondo/internal/service/users"
	"github.com/artemlavr/giocondo/internal/service/watcher"
	"github.com/artemlavr/giocondo/internal/transport/grpc/nodehub"
	"github.com/artemlavr/giocondo/internal/worker/events"
	"github.com/artemlavr/giocondo/internal/worker/tasks"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// App owns resources and lifecycle; service configuration is immutable after New.
type App struct {
	cfg Config
	db  *sqlite.DB // opened by Run; Dependencies.DB stays owned by the caller

	repositoriesOnce sync.Once
	repos            Repositories

	hub                      *nodehub.Hub
	allocator                nodeconfig.Allocator // one for config preparation and credential issuance
	hooks                    *events.Sender
	adminHandler, subHandler http.Handler
	tls                      *tls.Config
	background               context.Context //nolint:containedctx // Lifecycle root: Close cancels it, then joins workers.
	cancel                   context.CancelFunc
	mu                       sync.Mutex
	closed, running          bool
	workers                  sync.WaitGroup
	grpc                     *grpc.Server
	servers                  []*http.Server
	closeOnce                sync.Once
	closeErr                 error
	deps                     Dependencies
	hooksForServices         operation.Hooks
	nodeLock                 operation.NodeStateLock
	handlers                 sync.WaitGroup
	executorOnce             sync.Once
	executor                 *tasks.Executor
	credentials              *nodeconfig.Coordinator
	hosts                    *hosts.Service
	settings                 *settings.Service
	queries                  *nodes.Reader
	provisioning             *provisioning.Service
	probes                   *nodeprobe.Service
	network                  *network.Service
	nodes                    *nodes.Service
	nodeState                *nodestate.Service
	health                   *healthcheck.Service
	users                    *users.Service
	tokens                   *tokens.Service
	watcher                  *watcher.Service
	serviceOnce              struct {
		credentials  sync.Once
		hosts        sync.Once
		settings     sync.Once
		queries      sync.Once
		provisioning sync.Once
		probes       sync.Once
		network      sync.Once
		nodes        sync.Once
		nodeState    sync.Once
		health       sync.Once
		users        sync.Once
		tokens       sync.Once
		watcher      sync.Once
	}
}

// New creates the panel without opening anything. Ports missing from d are built from cfg; a facility
// whose settings are absent stays disabled, so operator commands with a partial Config keep DNS, probes and
// webhooks off. Services are initialized once. Run opens the database and starts the servers; d.DB and
// d.Repositories are for service-only use (operator commands and tests) and remain owned by the caller.
func New(cfg Config, d Dependencies) *App {
	background, cancel := context.WithCancel(context.Background())
	a := &App{cfg: cfg, background: background, cancel: cancel}

	a.deps = a.withClients(d)

	clock := d.Now
	if clock == nil {
		clock = time.Now
	}

	now := func() time.Time { return clock().UTC() }

	// Without an explicit Kick, changes wake the hub once Run has created it; operator commands rely on
	// the periodic reconciliation instead.
	kick := d.Kick
	if kick == nil {
		kick = func() {
			if a.hub != nil {
				a.hub.Kick()
			}
		}
	}

	emit := func(e panel.Event) {
		if a.deps.Notify != nil {
			e.Time = now()
			a.deps.Notify(e)
		}
	}
	a.hooksForServices = operation.Hooks{
		Now:      now,
		Kick:     kick,
		Emit:     emit,
		EmitUser: func(typ string, v panel.UserView) { emit(panel.Event{Type: typ, User: &v}) },
	}

	return a
}

// Run opens the database, starts the servers and workers and blocks until ctx ends or a server fails.
func (a *App) Run(ctx context.Context) (err error) {
	a.mu.Lock()
	if a.closed || a.running {
		a.mu.Unlock()

		return errors.New("panel already running or closed")
	}

	if a.deps.DB != nil || a.deps.Repositories.supplied() {
		a.mu.Unlock()

		return errors.New("panel server owns its database: do not supply Dependencies.DB or Repositories to Run")
	}

	defer func() { err = errors.Join(err, a.Close()) }() //nolint:contextcheck // Close owns its shutdown timeout.

	a.running = true
	errc := make(chan error, 3)
	// Preparation is serialized with Close so no resource opened here can escape shutdown.
	err = a.prepare(ctx)
	if err == nil {
		err = a.start(errc)
	}
	a.mu.Unlock()

	// Cancellation before startup completed is a requested stop, like cancellation while serving.
	if err != nil && ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return nil
	}

	if err != nil {
		return err
	}

	select {
	case err = <-errc:
		return err
	case <-ctx.Done():
		return nil
	case <-a.background.Done():
		return nil
	}
}

// Close cancels requests and workers, stops the servers, waits for active handlers and background
// tasks and closes the database. Repeated calls return the first result.
func (a *App) Close() error {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.cancel()
		a.mu.Unlock()

		if a.grpc != nil {
			a.grpc.Stop()
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		for _, srv := range a.servers {
			if e := srv.Shutdown(ctx); e != nil {
				a.closeErr = errors.Join(a.closeErr, e)
				_ = srv.Close()
			}
		}

		a.handlers.Wait()
		a.TaskExecutor().Close()
		a.workers.Wait()

		if a.db != nil {
			a.closeErr = errors.Join(a.closeErr, a.db.Close())
		}
	})

	return a.closeErr
}

// prepare builds the server dependencies before any service, handler or worker exists.
func (a *App) prepare(ctx context.Context) error {
	st, err := openDatabase(ctx, a.cfg.DB)
	if err != nil {
		return err
	}

	a.db = st
	a.hub = &nodehub.Hub{State: a.NodeState()}

	if err := a.addExternalProbe(); err != nil {
		return err
	}

	return a.configureServers(ctx)
}

func (a *App) trackHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		if a.closed {
			a.mu.Unlock()
			http.Error(w, "application is stopping", http.StatusServiceUnavailable)

			return
		}

		a.handlers.Add(1)

		a.mu.Unlock()
		defer a.handlers.Done()

		// A fresh identifier, never the client's header, so a client cannot forge log correlation.
		h.ServeHTTP(w, r.WithContext(logger.With(r.Context(), "request_id", logger.NewRequestID())))
	})
}
