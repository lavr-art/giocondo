package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/healthcheck"
)

func runProbe(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	listen := fs.String("listen", ":9444", "HTTPS listener")
	cert := fs.String("cert", "", "TLS server certificate")
	key := fs.String("key", "", "TLS server private key")
	allow := fs.String("allow", "", "comma-separated IP:port/protocol targets")
	if err := fs.Parse(args); err != nil {
		return err
	}
	token := os.Getenv("PANEL_PROBE_TOKEN")
	if token == "" || *cert == "" || *key == "" || *allow == "" {
		return fmt.Errorf("probe requires PANEL_PROBE_TOKEN, -cert, -key and -allow")
	}
	allowed := map[string]bool{}
	for _, target := range strings.Split(*allow, ",") {
		allowed[strings.TrimSpace(target)] = true
	}
	checker := &healthcheck.Local{Binary: envOr("PANEL_SINGBOX_BIN", "/usr/lib/giocondo-vpn/sing-box"), ChallengeURL: os.Getenv("PANEL_PROBE_CHALLENGE_URL"), RootCAFile: os.Getenv("PANEL_PROBE_CA")}
	srv := &http.Server{Addr: *listen, Handler: healthcheck.Handler(token, allowed, checker), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute}
	return srv.ListenAndServeTLS(*cert, *key)
}
