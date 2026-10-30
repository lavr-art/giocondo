package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/artemlavr/giocondo/internal/servicecheck"
)

func runCheck(args []string) error {
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
		addr = "127.0.0.1:8081"
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := servicecheck.WaitPanel(ctx, addr); err != nil {
		return err
	}
	fmt.Println("panel HTTP readiness confirmed")
	return nil
}
