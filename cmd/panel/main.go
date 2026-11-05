// Command panel is the control plane: API, web interface, subscriptions, node management.
//
// Without a command it runs the panel (HTTP + gRPC for agents). Operator commands are listed in commands.go
// and by `panel -h`.
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

	"github.com/artemlavr/giocondo/internal/app/panelapp"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: panelapp.ParseLevel(os.Getenv("PANEL_LOG_LEVEL"))}))
	slog.SetDefault(logger)

	cmd, args, err := lookup(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "panel: %v\n\n", err)
		printUsage(os.Stderr)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = cmd.run(ctx, args, os.Stdin, os.Stdout)
	stop()

	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
	case cmd.name == "":
		slog.Error("panel failed", "err", err)
		os.Exit(1)
	default:
		fmt.Fprintln(os.Stderr, "panel:", err)
		os.Exit(1)
	}
}
