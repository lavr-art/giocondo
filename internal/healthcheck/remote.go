package healthcheck

import (
	"bytes"
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/singbox"
)

var ErrUnavailable = errors.New("probe point unavailable")

type Remote struct {
	URL, Token string
	Client     *http.Client
}

func (r *Remote) Check(ctx context.Context, in Request) error {
	if !strings.HasPrefix(r.URL, "https://") {
		return ErrUnavailable
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
		pub, err := singbox.WGPublicKey(w.PrivateKey)
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
	req, err := http.NewRequestWithContext(cctx, "POST", r.URL, bytes.NewReader(data))
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+r.Token)
	req.Header.Set("Content-Type", "application/json")
	client := r.Client
	if client == nil {
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, err := client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != 200 {
		return ErrUnavailable
	}
	var result struct {
		Error string `json:"error"`
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&result); err != nil {
		return ErrUnavailable
	}
	if result.Error != "" {
		return errors.New(result.Error)
	}
	return nil
}

// Handler accepts only explicitly configured IP:port/protocol tuples.
func Handler(token string, allowed map[string]bool, c Checker) http.Handler {
	sem := make(chan struct{}, 4)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method != "POST" {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var req Request
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		ip, err := netip.ParseAddr(req.IP)
		if err != nil || !ip.Is4() || !allowed[TargetKey(req)] {
			http.Error(w, "target not allowed", http.StatusForbidden)
			return
		}
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		default:
			http.Error(w, "probe busy", http.StatusServiceUnavailable)
			return
		}
		err = c.Check(r.Context(), req)
		if errors.Is(err, ErrUnavailable) {
			http.Error(w, "probe unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"error": SafeError(err)})
	})
}

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
	return &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConfig}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
