// Package servicecheck provides non-mutating checks used after package upgrades.
package servicecheck

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Environment reads the running service's effective environment, including systemd overrides.
// It never evaluates shell expressions or modifies the caller's environment.
func Environment(pid int) (func(string) string, error) {
	if pid <= 0 {
		return nil, errors.New("a running service PID is required")
	}
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ")
	if err != nil {
		return nil, fmt.Errorf("read service environment: %w", err)
	}
	values := map[string]string{}
	for _, item := range strings.Split(string(b), "\x00") {
		k, v, ok := strings.Cut(item, "=")
		if ok {
			values[k] = v
		}
	}
	return func(k string) string { return values[k] }, nil
}

// WaitPanel checks the subscription handler, which starts only after database initialization.
func WaitPanel(ctx context.Context, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	if host == "::" {
		host = "::1"
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	challenge := hex.EncodeToString(nonce[:])
	endpoint := "http://" + net.JoinHostPort(host, port) + "/health/challenge?challenge=" + challenge
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode == http.StatusOK && string(body) == challenge {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("panel readiness not confirmed: %w", ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}
