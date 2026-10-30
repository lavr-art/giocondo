// Command panel is the control plane: API, web interface, subscriptions, node management.
//
//	panel                                  run the panel (HTTP + gRPC for agents)
//	panel ca init   -dir pki               create a private CA
//	panel ca server -dir pki -host H[,H]   panel certificate for agents
//	panel ca agent  -dir pki -node NAME    node agent certificate (CN = node name)
//	panel token create -name N -scope S    issue a REST API token (monitor | node-sync | admin)
//	panel local-node -host H               make the panel machine a node (as root; agent from giocondo-vpn-agent)
//	panel provision ssh -node N -host H    install the agent on your own server over SSH
//	panel provision cloud-init -node N     user-data for a new cloud server
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"

	"github.com/artemlavr/giocondo/internal/admin"
	"github.com/artemlavr/giocondo/internal/api"
	"github.com/artemlavr/giocondo/internal/dns"
	"github.com/artemlavr/giocondo/internal/healthcheck"
	"github.com/artemlavr/giocondo/internal/nodeapi/pb"
	"github.com/artemlavr/giocondo/internal/nodeapi/pki"
	"github.com/artemlavr/giocondo/internal/nodehub"
	"github.com/artemlavr/giocondo/internal/store"
	"github.com/artemlavr/giocondo/internal/subscription"
	"github.com/artemlavr/giocondo/internal/web"
	"github.com/artemlavr/giocondo/internal/webhook"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLevel(os.Getenv("PANEL_LOG_LEVEL"))}))
	slog.SetDefault(logger)

	if handled, err := runSubcommand(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "panel:", err)
			os.Exit(1)
		}
		return
	}

	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("panel starting", "version", version)
	if err := run(ctx); err != nil {
		slog.Error("panel failed", "err", err)
		stop()
		os.Exit(1) //nolint:gocritic // stop() is called above
	}
	slog.Info("panel stopped")
}

// runSubcommand runs service commands; false means there is no command and the panel starts.
func runSubcommand(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "check":
		return true, runCheck(args[1:])
	case "probe":
		return true, runProbe(args[1:])
	case "ca":
		return true, runCA(args[1:])
	case "token":
		return true, runToken(args[1:], os.Stdout)
	case "provision":
		return true, runProvision(args[1:], os.Stdin, os.Stdout)
	case "local-node":
		return true, runLocalNode(args[1:], os.Stdout, admin.DefaultLocalRuntime())
	}
	return false, nil
}

func run(ctx context.Context) error {
	dbPath := envOr("PANEL_DB", defaultDB())
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open store %s: %w", dbPath, err)
	}
	defer func() { _ = st.Close() }()
	slog.Info("store ready", "path", dbPath)

	hub := &nodehub.Hub{Store: st}
	publicURL := strings.TrimRight(os.Getenv("PANEL_PUBLIC_URL"), "/")
	// Subscriptions live on their own server and usually their own domain, so the admin address is not
	// exposed to clients. Without PANEL_SUB_URL links are built from PANEL_PUBLIC_URL.
	subURL := strings.TrimRight(envOr("PANEL_SUB_URL", publicURL), "/")
	svc := &admin.Service{
		Background: ctx,
		Store:      st,
		Kick:       hub.Kick,
		SubURL:     subURL,
		AgentAddr:  agentAddr(publicURL),
		AptRepo:    os.Getenv("PANEL_APT_REPO"),
		PKIDir:     envOr("PANEL_PKI_DIR", defaultPKIDir()),
	}
	svc.HealthInterval = admin.DefaultHealthInterval
	if raw := os.Getenv("PANEL_HEALTH_INTERVAL"); raw != "" {
		d, e := time.ParseDuration(raw)
		if e != nil || d <= 0 {
			return fmt.Errorf("invalid PANEL_HEALTH_INTERVAL")
		}
		svc.HealthInterval = d
	}
	svc.Checker = &healthcheck.Local{Binary: envOr("PANEL_SINGBOX_BIN", "/usr/lib/giocondo-vpn/sing-box"), ChallengeURL: envOr("PANEL_PROBE_CHALLENGE_URL", subURL+"/health/challenge"), RootCAFile: os.Getenv("PANEL_PROBE_CA")}
	svc.ProbePoints = map[string]healthcheck.Checker{}
	if point := os.Getenv("PANEL_PROBE_URL"); point != "" {
		if !strings.HasPrefix(point, "https://") || os.Getenv("PANEL_PROBE_TOKEN") == "" {
			return fmt.Errorf("external probe requires HTTPS URL and PANEL_PROBE_TOKEN")
		}
		client, e := healthcheck.HTTPClient(os.Getenv("PANEL_PROBE_CA"))
		if e != nil {
			return e
		}
		svc.ProbePoints["external"] = &healthcheck.Remote{URL: point, Token: os.Getenv("PANEL_PROBE_TOKEN"), Client: client}
	}
	if domain := os.Getenv("PANEL_DNS_DOMAIN"); domain != "" {
		if os.Getenv("PANEL_CLOUDFLARE_ZONE_ID") == "" || os.Getenv("PANEL_CLOUDFLARE_API_TOKEN") == "" {
			return fmt.Errorf("managed DNS requires Cloudflare zone and token")
		}
		svc.DNSDomain = domain
		svc.DNS = &dns.Cloudflare{ZoneID: os.Getenv("PANEL_CLOUDFLARE_ZONE_ID"), Token: os.Getenv("PANEL_CLOUDFLARE_API_TOKEN"), BaseURL: os.Getenv("PANEL_DNS_API_URL")}
		svc.ResolveA = admin.ResolveAuthoritative
		if server := os.Getenv("PANEL_DNS_VERIFY_SERVER"); server != "" {
			svc.ResolveA = func(ctx context.Context, host, ip string) error {
				resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, network, server)
				}}
				ips, e := resolver.LookupHost(ctx, host)
				if e != nil {
					return e
				}
				for _, a := range ips {
					if a == ip {
						return nil
					}
				}
				return fmt.Errorf("verification DNS does not return new IP")
			}
		}
		if err := svc.RecoverNetworkOperations(ctx); err != nil {
			return fmt.Errorf("recover IP operations: %w", err)
		}
	}
	if subURL == "" {
		slog.Warn("PANEL_SUB_URL and PANEL_PUBLIC_URL are not set: subscription links will be relative")
	}
	if urls := splitList(os.Getenv("PANEL_WEBHOOK_URL")); len(urls) > 0 {
		hooks := webhook.New(urls, os.Getenv("PANEL_WEBHOOK_SECRET"), slog.Default())
		go hooks.Run(ctx)
		svc.Notify = func(e admin.Event) { hooks.Send(e) }
		slog.Info("webhooks enabled", "urls", len(urls))
	}
	if v := os.Getenv("PANEL_AUTODELETE_EXPIRED_DAYS"); v != "" {
		days, err := strconv.Atoi(v)
		if err != nil || days < 0 {
			return fmt.Errorf("PANEL_AUTODELETE_EXPIRED_DAYS: want a number of days, got %q", v)
		}
		svc.AutoDeleteExpiredDays = days
	}

	adminMux := http.NewServeMux()
	if os.Getenv("PANEL_API_KEY") != "" {
		slog.Warn("PANEL_API_KEY is no longer used: create API tokens with `panel token create` or in the web interface")
	}
	(&api.Handler{Admin: svc}).Register(adminMux)
	password := os.Getenv("PANEL_ADMIN_PASSWORD")
	if password == "" {
		slog.Warn("PANEL_ADMIN_PASSWORD is not set: web interface is disabled")
	}
	// Admin UI and API live on a secret path; "/" serves a stub so scanners do not see the panel.
	adminPath := normalizeBase(os.Getenv("PANEL_ADMIN_PATH"))
	if adminPath == "" {
		slog.Warn("PANEL_ADMIN_PATH is not set: admin interface and API are served at /")
	}
	ui, err := web.New(web.Config{
		Admin: svc, Password: password, SessionSecret: os.Getenv("PANEL_SESSION_SECRET"),
		SecureCookie: strings.HasPrefix(publicURL, "https://"), Base: adminPath,
	})
	if err != nil {
		return err
	}
	ui.Register(adminMux)

	subMux := http.NewServeMux()
	(&subscription.Handler{Source: st, Title: envOr("PANEL_SUB_TITLE", "giocondo-vpn")}).Register(subMux)
	subMux.HandleFunc("GET /health/challenge", healthcheck.ChallengeHandler)

	go svc.Watch(ctx, time.Minute)
	go svc.CheckNodes(ctx, svc.HealthInterval)
	go hub.Reconcile(ctx, nodehub.DefaultResync)

	// HTTPS is terminated by the reverse proxy in front of the panel: both servers listen on plain HTTP.
	errc := make(chan error, 3)
	adminSrv := serveHTTP("admin", envOr("PANEL_LISTEN", "127.0.0.1:8080"), adminHandler(adminPath, adminMux), errc)
	subSrv := serveHTTP("subscription", envOr("PANEL_SUB_LISTEN", "127.0.0.1:8081"), subMux, errc)

	grpcSrv, err := startGRPC(hub, errc)
	if err != nil {
		return err
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if grpcSrv != nil {
		grpcSrv.Stop() // Sync streams are endless, GracefulStop would wait for them forever
	}
	return errors.Join(adminSrv.Shutdown(shutdownCtx), subSrv.Shutdown(shutdownCtx))
}

func serveHTTP(name, addr string, h http.Handler, errc chan<- error) *http.Server {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		slog.Info("http listening", "server", name, "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- fmt.Errorf("%s http: %w", name, err)
		}
	}()
	return srv
}

// startGRPC starts the agent server on PANEL_GRPC_LISTEN (default :9443).
// PANEL_GRPC_LISTEN=off disables it, e.g. on a subscription-only panel.
func startGRPC(hub *nodehub.Hub, errc chan<- error) (*grpc.Server, error) {
	addr := envOr("PANEL_GRPC_LISTEN", ":9443")
	if addr == "off" {
		slog.Warn("grpc for agents is disabled")
		return nil, nil
	}
	p := pki.Paths{Dir: envOr("PANEL_PKI_DIR", defaultPKIDir())}
	tlsCfg, err := pki.ServerTLS(p.CACert(), p.ServerCert(), p.ServerKey())
	if err != nil {
		return nil, fmt.Errorf("agent TLS (create it with `panel ca init` and `panel ca server`, or set PANEL_GRPC_LISTEN=off): %w", err)
	}
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("grpc listen: %w", err)
	}
	srv := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(tlsCfg)),
		// Agents ping every 30 s (cmd/agent); more often than every 20 s is not allowed.
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{MinTime: 20 * time.Second, PermitWithoutStream: true}),
		// Stop waits for Sync handlers: they mark the node offline, and the database is closed afterwards.
		grpc.WaitForHandlers(true),
	)
	pb.RegisterNodeServiceServer(srv, hub)
	go func() {
		slog.Info("grpc listening", "addr", addr)
		if err := srv.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			errc <- fmt.Errorf("grpc: %w", err)
		}
	}()
	return srv, nil
}

// agentAddr is the agent gRPC address for install instructions: PANEL_AGENT_ADDR or
// the PANEL_PUBLIC_URL host with the PANEL_GRPC_LISTEN port.
func agentAddr(publicURL string) string {
	if v := os.Getenv("PANEL_AGENT_ADDR"); v != "" {
		return v
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	_, port, err := net.SplitHostPort(envOr("PANEL_GRPC_LISTEN", ":9443"))
	if err != nil {
		port = "9443"
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// splitList returns comma-separated values without empty ones.
func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// debStateDir is the panel data directory of the giocondo-vpn-panel package. If it exists, commands without
// PANEL_DB / PANEL_PKI_DIR (e.g. `sudo -u giocondo-vpn giocondo-vpn-panel token create`) use it
// instead of files in the current directory.
const debStateDir = "/var/lib/giocondo-vpn/panel"

func defaultDB() string {
	if st, err := os.Stat(debStateDir); err == nil && st.IsDir() {
		return debStateDir + "/panel.db"
	}
	return "panel.db"
}

func defaultPKIDir() string {
	if st, err := os.Stat(debStateDir); err == nil && st.IsDir() {
		return debStateDir + "/pki"
	}
	return "pki"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// parseLevel maps an env value to a slog level; info by default.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
