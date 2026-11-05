// Command agent is the node agent: it applies the sing-box config and reports statistics to the panel.

// The agent connects to the panel itself (AGENT_PANEL_ADDR, several comma-separated addresses allowed:
// primary and fallbacks) over gRPC with mTLS and holds the Sync stream. No management port is opened on the node.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/artemlavr/giocondo/internal/app/agent"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	check := flag.Bool("check", false, "check local engine readiness without changing state")
	pid := flag.Int("pid", 0, "running agent PID for -check")
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Println(version)
	case *check:
		if err := agent.Check(*pid); err != nil {
			fmt.Fprintln(os.Stderr, "agent readiness:", err)
			os.Exit(1)
		}

		fmt.Println("local engine ready; panel synchronization is not confirmed by this check")
	default:
		if err := serve(); err != nil {
			slog.Error("agent failed", "err", err)
			os.Exit(1)
		}
	}
}

func serve() error {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: agent.ParseLevel(os.Getenv("AGENT_LOG_LEVEL"))})))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("agent starting", "version", version)

	cfg, err := agent.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}

	app := agent.New(cfg, version)
	if err := errors.Join(app.Run(ctx), app.Close()); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	slog.Info("agent stopped")

	return nil
}
