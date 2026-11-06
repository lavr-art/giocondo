package panelapp

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/service/healthcheck"
)

// Defaults of the listen addresses. Operator commands use the same defaults for these settings.
const (
	DefaultAdminListen = "127.0.0.1:8080"
	DefaultSubListen   = "127.0.0.1:8081"
	DefaultGRPCListen  = ":9443"
)

// Config is a validated snapshot of panel settings; services receive only their own options.
type Config struct {
	// DrainDelay is not read from the environment: zero means network.DefaultDrainDelay. Tests shorten it.
	DrainDelay time.Duration

	DB        string
	PKIDir    string
	PublicURL string
	SubURL    string
	AgentAddr string
	AptRepo   string

	AdminListen   string
	SubListen     string
	GRPCListen    string
	AdminPath     string
	Password      string
	SessionSecret string
	SubTitle      string

	SingboxBinary string
	ChallengeURL  string
	ProbeCA       string
	ProbeURL      string
	ProbeToken    string

	DNSDomain       string
	DNSZoneID       string
	DNSToken        string
	DNSAPIURL       string
	DNSVerifyServer string

	WebhookURLs           []string
	WebhookSecret         string
	HealthInterval        time.Duration
	AutoDeleteExpiredDays int
	DeprecatedAPIKey      bool
}

// LoadConfig loads configuration from the environment.
func LoadConfig(getenv func(string) string) (Config, error) {
	or := func(k, def string) string {
		if v := getenv(k); v != "" {
			return v
		}

		return def
	}

	publicURL := strings.TrimRight(getenv("PANEL_PUBLIC_URL"), "/")
	subURL := strings.TrimRight(or("PANEL_SUB_URL", publicURL), "/")

	c := Config{
		DB:               or("PANEL_DB", DefaultDB()),
		PKIDir:           or("PANEL_PKI_DIR", DefaultPKIDir()),
		PublicURL:        publicURL,
		SubURL:           subURL,
		AptRepo:          getenv("PANEL_APT_REPO"),
		AgentAddr:        AgentAddress(getenv, publicURL),
		AdminListen:      or("PANEL_LISTEN", DefaultAdminListen),
		SubListen:        or("PANEL_SUB_LISTEN", DefaultSubListen),
		GRPCListen:       or("PANEL_GRPC_LISTEN", DefaultGRPCListen),
		AdminPath:        getenv("PANEL_ADMIN_PATH"),
		Password:         getenv("PANEL_ADMIN_PASSWORD"),
		SessionSecret:    getenv("PANEL_SESSION_SECRET"),
		SubTitle:         getenv("PANEL_SUB_TITLE"),
		SingboxBinary:    or("PANEL_SINGBOX_BIN", "/usr/lib/giocondo-vpn/sing-box"),
		ChallengeURL:     or("PANEL_PROBE_CHALLENGE_URL", subURL+"/health/challenge"),
		ProbeCA:          getenv("PANEL_PROBE_CA"),
		ProbeURL:         getenv("PANEL_PROBE_URL"),
		ProbeToken:       getenv("PANEL_PROBE_TOKEN"),
		DNSDomain:        getenv("PANEL_DNS_DOMAIN"),
		DNSZoneID:        getenv("PANEL_CLOUDFLARE_ZONE_ID"),
		DNSToken:         getenv("PANEL_CLOUDFLARE_API_TOKEN"),
		DNSAPIURL:        getenv("PANEL_DNS_API_URL"),
		DNSVerifyServer:  getenv("PANEL_DNS_VERIFY_SERVER"),
		WebhookURLs:      SplitList(getenv("PANEL_WEBHOOK_URL")),
		WebhookSecret:    getenv("PANEL_WEBHOOK_SECRET"),
		HealthInterval:   healthcheck.DefaultHealthInterval,
		DeprecatedAPIKey: getenv("PANEL_API_KEY") != "",
	}
	if raw := getenv("PANEL_HEALTH_INTERVAL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return c, fmt.Errorf("invalid PANEL_HEALTH_INTERVAL")
		}

		c.HealthInterval = d
	}

	if raw := getenv("PANEL_AUTODELETE_EXPIRED_DAYS"); raw != "" {
		days, err := strconv.Atoi(raw)
		if err != nil || days < 0 {
			return c, fmt.Errorf("PANEL_AUTODELETE_EXPIRED_DAYS: want a number of days, got %q", raw)
		}

		c.AutoDeleteExpiredDays = days
	}

	if c.ProbeURL != "" && (!strings.HasPrefix(c.ProbeURL, "https://") || c.ProbeToken == "") {
		return c, fmt.Errorf("external probe requires HTTPS URL and PANEL_PROBE_TOKEN")
	}

	if c.DNSDomain != "" && (c.DNSZoneID == "" || c.DNSToken == "") {
		return c, fmt.Errorf("managed DNS requires Cloudflare zone and token")
	}

	return c, nil
}

// AgentAddress returns the panel gRPC address agents connect to: PANEL_AGENT_ADDR, or the public URL host
// with the gRPC listen port. Without a public URL it is empty.
func AgentAddress(getenv func(string) string, publicURL string) string {
	if v := getenv("PANEL_AGENT_ADDR"); v != "" {
		return v
	}

	u, err := url.Parse(publicURL)
	if err != nil || u.Hostname() == "" {
		return ""
	}

	addr := getenv("PANEL_GRPC_LISTEN")
	if addr == "" {
		addr = DefaultGRPCListen
	}

	// With gRPC off the agent address still carries the default port, where the panel normally listens.
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		_, port, _ = net.SplitHostPort(DefaultGRPCListen)
	}

	return net.JoinHostPort(u.Hostname(), port)
}

// SplitList splits a comma-separated list.
func SplitList(s string) []string {
	var out []string

	for v := range strings.SplitSeq(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}

	return out
}

const debStateDir = "/var/lib/giocondo-vpn/panel"

// DefaultDB returns the default database path.
func DefaultDB() string {
	if st, err := os.Stat(debStateDir); err == nil && st.IsDir() {
		return debStateDir + "/panel.db"
	}

	return "panel.db"
}

// DefaultPKIDir returns the default PKI directory.
func DefaultPKIDir() string {
	if st, err := os.Stat(debStateDir); err == nil && st.IsDir() {
		return debStateDir + "/pki"
	}

	return "pki"
}

// ParseLevel parses a log level name.
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
