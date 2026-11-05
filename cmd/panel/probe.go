package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/platform/protocheck"
	"github.com/artemlavr/giocondo/internal/transport/http/probeapi"
)

const probeShutdownTimeout = 5 * time.Second

func runProbe(ctx context.Context, args []string, _ io.Reader, _ io.Writer) error {
	fs := flag.NewFlagSet("panel probe", flag.ContinueOnError)
	listen := fs.String("listen", ":9444", "HTTPS listener")
	cert := fs.String("cert", "", "TLS server certificate")
	key := fs.String("key", "", "TLS server private key")
	allow := fs.String("allow", "", "comma-separated IP:port/protocol targets")

	if err := fs.Parse(args); err != nil {
		return err
	}

	token := os.Getenv("PANEL_PROBE_TOKEN")
	if token == "" || *cert == "" || *key == "" || *allow == "" {
		return errors.New("probe requires PANEL_PROBE_TOKEN, -cert, -key and -allow")
	}

	allowed := map[string]bool{}
	for target := range strings.SplitSeq(*allow, ",") {
		allowed[strings.TrimSpace(target)] = true
	}

	checker := &protocheck.Local{
		Binary:       envOr("PANEL_SINGBOX_BIN", "/usr/lib/giocondo-vpn/sing-box"),
		ChallengeURL: os.Getenv("PANEL_PROBE_CHALLENGE_URL"),
		RootCAFile:   os.Getenv("PANEL_PROBE_CA"),
	}
	srv := &http.Server{
		Addr:              *listen,
		Handler:           probeapi.Handler(token, allowed, checker),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       time.Minute,
	}

	// main handles SIGTERM through ctx, so the server must stop on cancellation; it ends with
	// ErrServerClosed after Shutdown, which the buffered channel absorbs.
	served := make(chan error, 1)
	go func() { served <- srv.ListenAndServeTLS(*cert, *key) }()

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeShutdownTimeout)
	defer cancel()

	return srv.Shutdown(shutdownCtx)
}
