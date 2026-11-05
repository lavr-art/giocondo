package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/artemlavr/giocondo/internal/app/panelapp"
)

func runServe(ctx context.Context, args []string, _ io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("panel", flag.ContinueOnError)
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.Usage = func() {
		printUsage(fs.Output())
		_, _ = fmt.Fprintln(fs.Output(), "\nflags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *showVersion {
		_, err := fmt.Fprintln(stdout, version)

		return err
	}

	slog.Info("panel starting", "version", version)

	cfg, err := panelapp.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}

	app := panelapp.New(cfg, panelapp.Dependencies{})
	if err := app.Run(ctx); err != nil {
		return err
	}

	slog.Info("panel stopped")

	return nil
}
