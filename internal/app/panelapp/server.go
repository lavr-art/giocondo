package panelapp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	"github.com/artemlavr/giocondo/internal/repository/pkifs"
	"github.com/artemlavr/giocondo/internal/service/subscription"
	"github.com/artemlavr/giocondo/internal/transport/grpc/pb"
	"github.com/artemlavr/giocondo/internal/transport/http/adminroute"
	"github.com/artemlavr/giocondo/internal/transport/http/api"
	"github.com/artemlavr/giocondo/internal/transport/http/probeapi"
	"github.com/artemlavr/giocondo/internal/transport/http/subapi"
	"github.com/artemlavr/giocondo/internal/transport/http/web"
	"github.com/artemlavr/giocondo/pkg/logger"
)

func (a *App) configureServers(ctx context.Context) (err error) {
	if a.deps.DNS != nil {
		if err = a.NetworkService().RecoverNetworkOperations(ctx); err != nil {
			return fmt.Errorf("recover IP operations: %w", err)
		}
	}

	if a.adminHandler == nil {
		if a.adminHandler, err = a.adminHTTP(); err != nil {
			return err
		}
	}

	if a.subHandler == nil {
		subMux := http.NewServeMux()
		repos := a.repositories()
		source := subscription.Source{
			Users: repos.Users, Nodes: repos.Nodes, Hosts: repos.Hosts, Settings: a.SettingsService(),
		}
		(&subapi.Handler{Source: source}).Register(subMux)
		subMux.HandleFunc("GET /health/challenge", probeapi.ChallengeHandler)
		a.subHandler = subMux
	}

	if a.cfg.GRPCListen != "off" {
		p := pkifs.Paths{Dir: a.cfg.PKIDir}

		a.tls, err = pkifs.ServerTLS(p.CACert(), p.ServerCert(), p.ServerKey())
		if err != nil {
			return fmt.Errorf(
				"agent TLS (create it with `panel ca init` and `panel ca server`, or set PANEL_GRPC_LISTEN=off): %w",
				err,
			)
		}
	}

	return nil
}

// start is serialized with Close so no listener or worker can escape shutdown.
func (a *App) start(errc chan<- error) error {
	start := func(fn func()) { a.workers.Go(fn) }

	for _, v := range []struct {
		name, addr string
		handler    http.Handler
	}{
		{"admin", a.cfg.AdminListen, a.adminHandler}, {"subscription", a.cfg.SubListen, a.subHandler},
	} {
		lis, e := (&net.ListenConfig{}).Listen(a.background, "tcp", v.addr)
		if e != nil {
			return fmt.Errorf("%s http: %w", v.name, e)
		}

		srv := &http.Server{
			Addr:              v.addr,
			Handler:           a.trackHandler(v.handler),
			BaseContext:       func(net.Listener) context.Context { return a.background },
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       2 * time.Minute,
		}
		a.servers = append(a.servers, srv)

		start(func() {
			defer func() { _ = lis.Close() }()

			logger.FromCtx(a.background).Info("http listening", "server", v.name, "addr", lis.Addr())

			if e := srv.Serve(lis); e != nil && !errors.Is(e, http.ErrServerClosed) {
				errc <- fmt.Errorf("%s http: %w", v.name, e)
			}
		})
	}

	if a.cfg.GRPCListen != "off" {
		lis, e := (&net.ListenConfig{}).Listen(a.background, "tcp", a.cfg.GRPCListen)
		if e != nil {
			return fmt.Errorf("grpc listen: %w", e)
		}

		a.grpc = grpc.NewServer(
			grpc.Creds(credentials.NewTLS(a.tls)),
			grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 20 * time.Second, PermitWithoutStream: true}),
			grpc.WaitForHandlers(true),
		)
		pb.RegisterNodeServiceServer(a.grpc, a.hub)
		start(func() {
			defer func() { _ = lis.Close() }()

			if e := a.grpc.Serve(lis); e != nil && !errors.Is(e, grpc.ErrServerStopped) {
				errc <- fmt.Errorf("grpc: %w", e)
			}
		})
	}
	// Initialize service dependencies before launching periodic workers.
	a.WatcherService()
	a.HealthcheckService()
	a.startWorkers(start)

	return nil
}

// adminHTTP builds the admin API and web UI handler under the configured admin path.
func (a *App) adminHTTP() (http.Handler, error) {
	adminMux := http.NewServeMux()
	(&api.Handler{
		Dependencies: api.Dependencies{
			Users:        a.UserService(),
			Tokens:       a.TokenService(),
			Queries:      a.NodeQueries(),
			Nodes:        a.NodeService(),
			Hosts:        a.HostService(),
			Provisioning: a.ProvisioningService(),
			Network:      a.NetworkService(),
		},
	}).Register(adminMux)

	ui, err := web.New(
		web.Config{
			Dependencies: web.Dependencies{
				Users:        a.UserService(),
				Tokens:       a.TokenService(),
				Queries:      a.NodeQueries(),
				Nodes:        a.NodeService(),
				Hosts:        a.HostService(),
				Provisioning: a.ProvisioningService(),
				Network:      a.NetworkService(),
				Settings:     a.SettingsService(),
				Dashboard:    a.DashboardService(),
			},
			Password:      a.cfg.Password,
			SessionSecret: a.cfg.SessionSecret,
			SecureCookie:  strings.HasPrefix(a.cfg.PublicURL, "https://"),
			Base:          adminroute.NormalizeBase(a.cfg.AdminPath),
		},
	)
	if err != nil {
		return nil, err
	}

	ui.Register(adminMux)

	return adminroute.Handler(a.cfg.AdminPath, adminMux), nil
}
