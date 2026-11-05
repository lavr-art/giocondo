package agent

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
)

// Defaults that are not configurable through the environment.
const (
	defaultHealthInterval = time.Minute
	defaultSettleDelay    = 3 * time.Second
)

// Config is the agent configuration. LoadConfig fills it from AGENT_* variables; every
// value it leaves unset has its default there.
type Config struct {
	Panels                    []PanelAddress // primary address first, then fallbacks
	CAFile, CertFile, KeyFile string
	SingBoxBin, ConfigPath    string
	StatsAddr                 string // sing-box v2ray_api endpoint on loopback
	StateDir                  string // agent lock and per-engine journals

	// RestartCmd, StatusCmd and StopCmd control the sing-box service; they run without a shell.
	RestartCmd, StatusCmd, StopCmd []string

	// StatsInterval is how often counters are collected and sent; HealthInterval is how often
	// engine activity is checked and reported.
	StatsInterval, HealthInterval time.Duration
	// SettleDelay is the wait after a core restart before checking that the core is still active.
	SettleDelay time.Duration
}

// PanelAddress is a panel address and the name its certificate must carry.
type PanelAddress struct {
	Addr, ServerName string
}

// LoadConfig reads the configuration through getenv, normally os.Getenv or the environment of a
// running agent. It requires at least one AGENT_PANEL_ADDR and rejects malformed values.
func LoadConfig(getenv func(string) string) (Config, error) {
	or := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}

		return def
	}

	panels, err := panelAddresses(getenv("AGENT_PANEL_ADDR"), getenv("AGENT_PANEL_SERVER_NAME"))
	if err != nil {
		return Config{}, err
	}

	statsInterval, err := time.ParseDuration(or("AGENT_STATS_INTERVAL", "60s"))
	if err != nil || statsInterval < time.Second {
		return Config{}, fmt.Errorf("AGENT_STATS_INTERVAL: bad duration %q", getenv("AGENT_STATS_INTERVAL"))
	}

	return Config{
		Panels:         panels,
		CAFile:         or("AGENT_CA", "/etc/giocondo-vpn/agent/ca.crt"),
		CertFile:       or("AGENT_CERT", "/etc/giocondo-vpn/agent/agent.crt"),
		KeyFile:        or("AGENT_KEY", "/etc/giocondo-vpn/agent/agent.key"),
		SingBoxBin:     or("AGENT_SINGBOX_BIN", "/usr/lib/giocondo-vpn/sing-box"),
		ConfigPath:     or("AGENT_SINGBOX_CONFIG", "/etc/giocondo-vpn/sing-box/config.json"),
		StatsAddr:      or("AGENT_STATS_ADDR", "127.0.0.1:10085"),
		StateDir:       or("AGENT_STATE_DIR", "/var/lib/giocondo-vpn/agent"),
		RestartCmd:     strings.Fields(or("AGENT_RESTART_CMD", "systemctl restart giocondo-vpn-sing-box")),
		StatusCmd:      strings.Fields(or("AGENT_STATUS_CMD", "systemctl is-active --quiet giocondo-vpn-sing-box")),
		StopCmd:        strings.Fields(or("AGENT_STOP_CMD", "systemctl stop giocondo-vpn-sing-box")),
		StatsInterval:  statsInterval,
		HealthInterval: defaultHealthInterval,
		SettleDelay:    defaultSettleDelay,
	}, nil
}

// panelAddresses parses the comma-separated AGENT_PANEL_ADDR. One serverName applies to every address
// (one panel, one certificate); without it each address must match its own host.
func panelAddresses(list, serverName string) ([]PanelAddress, error) {
	var panels []PanelAddress

	for addr := range strings.SplitSeq(list, ",") {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}

		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("AGENT_PANEL_ADDR: %w", err)
		}

		if serverName != "" {
			host = serverName
		}

		panels = append(panels, PanelAddress{Addr: addr, ServerName: host})
	}

	if len(panels) == 0 {
		return nil, errors.New("AGENT_PANEL_ADDR is required (panel host:port[,host:port...])")
	}

	return panels, nil
}

// ParseLevel maps a log level name to a slog level; info by default.
func ParseLevel(s string) slog.Level {
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
