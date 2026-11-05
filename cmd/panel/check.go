package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/artemlavr/giocondo/internal/app/panelapp"
	"github.com/artemlavr/giocondo/internal/platform/servicecheck"
)

func runCheck(ctx context.Context, args []string, _ io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("panel check", flag.ContinueOnError)
	pid := fs.Int("pid", 0, "running panel PID (reads its effective systemd environment)")
	timeout := fs.Duration("timeout", 30*time.Second, "readiness timeout")

	if err := fs.Parse(args); err != nil {
		return err
	}

	getenv, err := servicecheck.Environment(*pid)
	if err != nil {
		return err
	}

	addr := getenv("PANEL_SUB_LISTEN")
	if addr == "" {
		addr = panelapp.DefaultSubListen
	}

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	if err := servicecheck.WaitPanel(ctx, addr); err != nil {
		return err
	}

	_, err = fmt.Fprintln(stdout, "panel HTTP readiness confirmed")

	return err
}
