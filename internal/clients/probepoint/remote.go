// Package probepoint asks an external probe point to check node endpoints, so availability is also
// confirmed from outside the panel's network.
package probepoint

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// Remote is an external probe point reached over HTTPS with a bearer token.
type Remote struct {
	URL, Token string
	Client     *http.Client
}

// Check performs the check.
func (r *Remote) Check(ctx context.Context, in probe.Request) error {
	if !strings.HasPrefix(r.URL, "https://") {
		return probe.ErrUnavailable
	}

	// Only public node parameters and the dedicated monitor credential leave the panel.
	in.Inbound.Reality = nil
	if in.Inbound.Hysteria2 != nil {
		h := *in.Inbound.Hysteria2
		h.KeyPEM = ""
		in.Inbound.Hysteria2 = &h
	}

	if in.Inbound.WireGuard != nil {
		w := *in.Inbound.WireGuard

		pub, err := protocol.WGPublicKey(w.PrivateKey)
		if err != nil {
			return err
		}

		in.WGPublicKey = pub
		w.PrivateKey = ""
		in.Inbound.WireGuard = &w
	}

	data, err := json.Marshal(in)
	if err != nil {
		return err
	}

	cctx, cancel := context.WithTimeout(ctx, 20_000_000_000)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodPost, r.URL, bytes.NewReader(data))
	if err != nil {
		return probe.ErrUnavailable
	}

	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Content-Type", "application/json")

	client := r.Client
	if client == nil {
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}

	res, err := client.Do(req)
	if err != nil {
		return probe.ErrUnavailable
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		return probe.ErrUnavailable
	}

	var result struct {
		Error string `json:"error"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&result); err != nil {
		return probe.ErrUnavailable
	}

	if result.Error != "" {
		return errors.New(result.Error)
	}

	return nil
}

// HTTPClient returns the shared HTTP client.
func HTTPClient(caFile string) (*http.Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}

	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}

		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}

		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("invalid probe CA")
		}

		tlsConfig.RootCAs = roots
	}

	return &http.Client{
		Timeout:       20 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: tlsConfig},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}
