// Package main runs the network benchmark harness.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/artemlavr/giocondo/test/network-e2e/internal/bench"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := bench.Run(ctx, os.Args[1:], os.Stdout)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}
