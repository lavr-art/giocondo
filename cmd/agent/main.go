// Command agent is the node agent: it applies the sing-box config and reports statistics to the panel.
//
// The agent connects to the panel itself (AGENT_PANEL_ADDR, several comma-separated addresses allowed:
// primary and fallbacks) over gRPC with mTLS and holds the Sync stream. No management port is opened on the node.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/artemlavr/giocondo/internal/agent"
	"github.com/artemlavr/giocondo/internal/agent/engine"
	"github.com/artemlavr/giocondo/internal/agent/singboxdriver"
	"github.com/artemlavr/giocondo/internal/nodeapi/pb"
	"github.com/artemlavr/giocondo/internal/nodeapi/pki"
	"github.com/artemlavr/giocondo/internal/nodeapi/v2raystats"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: parseLevel(os.Getenv("AGENT_LOG_LEVEL"))}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("agent starting", "version", version)
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("agent failed", "err", err)
		stop()
		os.Exit(1) //nolint:gocritic // stop() is called above
	}
	slog.Info("agent stopped")
}

type config struct {
	panels                         []panelAddr
	caFile, certFile, key          string
	singBoxBin, configPath         string
	statsAddr                      string
	stateDir                       string
	statsInterval                  time.Duration
	restartCmd, statusCmd, stopCmd []string
}

// panelAddr is a panel address and the name its certificate must carry.
type panelAddr struct {
	addr, serverName string
}

func loadConfig(getenv func(string) string) (config, error) {
	or := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return def
	}
	c := config{
		caFile:     or("AGENT_CA", "/etc/giocondo-vpn/agent/ca.crt"),
		certFile:   or("AGENT_CERT", "/etc/giocondo-vpn/agent/agent.crt"),
		key:        or("AGENT_KEY", "/etc/giocondo-vpn/agent/agent.key"),
		singBoxBin: or("AGENT_SINGBOX_BIN", "/usr/lib/giocondo-vpn/sing-box"),
		configPath: or("AGENT_SINGBOX_CONFIG", "/etc/giocondo-vpn/sing-box/config.json"),
		stateDir:   or("AGENT_STATE_DIR", "/var/lib/giocondo-vpn/agent"),
		stopCmd:    strings.Fields(or("AGENT_STOP_CMD", "systemctl stop giocondo-vpn-sing-box")),
		statsAddr:  or("AGENT_STATS_ADDR", "127.0.0.1:10085"),
		restartCmd: strings.Fields(or("AGENT_RESTART_CMD", "systemctl restart giocondo-vpn-sing-box")),
		statusCmd:  strings.Fields(or("AGENT_STATUS_CMD", "systemctl is-active --quiet giocondo-vpn-sing-box")),
	}
	// One AGENT_PANEL_SERVER_NAME for all addresses (one panel, one certificate), otherwise the address host.
	serverName := getenv("AGENT_PANEL_SERVER_NAME")
	for _, addr := range strings.Split(getenv("AGENT_PANEL_ADDR"), ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return c, fmt.Errorf("AGENT_PANEL_ADDR: %w", err)
		}
		if serverName != "" {
			host = serverName
		}
		c.panels = append(c.panels, panelAddr{addr: addr, serverName: host})
	}
	if len(c.panels) == 0 {
		return c, errors.New("AGENT_PANEL_ADDR is required (panel host:port[,host:port...])")
	}
	d, err := time.ParseDuration(or("AGENT_STATS_INTERVAL", "60s"))
	if err != nil || d < time.Second {
		return c, fmt.Errorf("AGENT_STATS_INTERVAL: bad duration %q", getenv("AGENT_STATS_INTERVAL"))
	}
	c.statsInterval = d
	return c, nil
}

func run(ctx context.Context) error {
	cfg, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}
	// One connection per address. grpc.NewClient is lazy: the socket is opened only when the agent
	// tries this address.
	var panels []agent.Panel
	for _, p := range cfg.panels {
		tlsCfg, err := pki.ClientTLS(cfg.caFile, cfg.certFile, cfg.key, p.serverName)
		if err != nil {
			return fmt.Errorf("tls: %w", err)
		}
		conn, err := grpc.NewClient(p.addr,
			grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
			// Ping every 30 s: a dead connection (IP change, NAT) is noticed within a minute.
			grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 20 * time.Second}),
			// gRPC's own backoff is no longer than the agent's: a recovered panel does not wait up to two minutes.
			grpc.WithConnectParams(grpc.ConnectParams{Backoff: backoff.Config{
				BaseDelay: time.Second, Multiplier: 1.6, Jitter: 0.2, MaxDelay: time.Minute,
			}, MinConnectTimeout: 20 * time.Second}),
		)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close() }()
		panels = append(panels, agent.Panel{Addr: p.addr, Client: pb.NewNodeServiceClient(conn)})
	}

	// sing-box v2ray_api listens on loopback only, there is no TLS.
	statsConn, err := grpc.NewClient(cfg.statsAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer func() { _ = statsConn.Close() }()

	lock, err := engine.Lock(cfg.stateDir)
	if err != nil {
		return fmt.Errorf("lock agent state: %w", err)
	}
	defer func() { _ = lock.Close() }()
	runtime := &engine.Runtime{
		Driver: &singboxdriver.Driver{Binary: cfg.singBoxBin, RestartCmd: cfg.restartCmd, StatusCmd: cfg.statusCmd, StopCmd: cfg.stopCmd,
			Collector: &singboxdriver.Collector{Client: v2raystats.NewStatsServiceClient(statsConn)}},
		ConfigPath: cfg.configPath, StateDir: filepath.Join(cfg.stateDir, "sing-box"), SettleDelay: 3 * time.Second,
	}
	engines, err := engine.NewManager(runtime)
	if err != nil {
		return err
	}
	a := &agent.Agent{Panels: panels, Engines: engines, Version: version, StatsInterval: cfg.statsInterval}

	addrs := make([]string, len(cfg.panels))
	for i, p := range cfg.panels {
		addrs[i] = p.addr
	}
	slog.Info("connecting to panel", "addrs", addrs)
	return a.Run(ctx)
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
