// Package probeapi serves the probe point API and the challenge that probes fetch through a VPN tunnel.
package probeapi

import (
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// Handler returns the HTTP handler.
func Handler(token string, allowed map[string]bool, c probe.Checker) http.Handler {
	sem := make(chan struct{}, 4)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)

			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)

			return
		}

		var req probe.Request

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
		if errors.Is(err, probe.ErrUnavailable) {
			http.Error(w, "probe unavailable", http.StatusServiceUnavailable)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"error": operation.SafeError(err)})
	})
}

// ChallengeHandler echoes a 32-byte hex challenge: a probe that fetches it through a VPN connection
// proves that the tunnel carries traffic.
func ChallengeHandler(w http.ResponseWriter, r *http.Request) {
	c := r.URL.Query().Get("challenge")

	b, err := hex.DecodeString(c)
	if err != nil || len(b) != 32 {
		http.Error(w, "invalid challenge", http.StatusBadRequest)

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, c) //nolint:gosec // c is a validated 64-character hex string served as text/plain.
}

// TargetKey returns a stable key for a target.
func TargetKey(r probe.Request) string {
	return net.JoinHostPort(r.IP, strconv.Itoa(int(r.Inbound.Port))) + "/" + string(r.Inbound.Type)
}
