// Package healthcheck probes the public protocol listeners, independently of agent Sync.
package healthcheck

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/singbox"
)

type Request struct {
	WGPublicKey string          `json:"wg_public_key,omitempty"`
	IP          string          `json:"ip"`
	Host        string          `json:"host"`
	Inbound     singbox.Inbound `json:"inbound"`
	User        singbox.User    `json:"user"`
}
type Checker interface {
	Check(context.Context, Request) error
}
type CheckerFunc func(context.Context, Request) error

func (f CheckerFunc) Check(ctx context.Context, r Request) error { return f(ctx, r) }

type Local struct {
	Binary       string
	ChallengeURL string
	RootCAFile   string
}

// ClientConfig routes a temporary HTTP proxy entirely through the requested protocol.
func ClientConfig(r Request, port int) ([]byte, error) {
	in := r.Inbound
	cfg := map[string]any{
		"log":      map[string]any{"disabled": true},
		"inbounds": []any{map[string]any{"type": "http", "listen": "127.0.0.1", "listen_port": port}},
		"route":    map[string]any{"final": "probe", "default_domain_resolver": "local"},
		"dns":      map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "local"}}},
	}
	switch in.Type {
	case singbox.TypeHysteria2:
		if in.Hysteria2 == nil {
			return nil, errors.New("missing hysteria2 parameters")
		}
		tls := map[string]any{"enabled": true, "server_name": r.Host}
		if in.Hysteria2.TLSMode() == singbox.TLSSelfSigned {
			// Trust exactly the node's existing certificate (no insecure UDP probe).
			tls["certificate"] = []string{in.Hysteria2.CertPEM}
		}
		out := map[string]any{"type": "hysteria2", "tag": "probe", "server": r.IP, "server_port": in.Port, "password": r.User.UUID, "tls": tls}
		if in.Hysteria2.ObfsPassword != "" {
			out["obfs"] = map[string]any{"type": "salamander", "password": in.Hysteria2.ObfsPassword}
		}
		cfg["outbounds"] = []any{out}
	case singbox.TypeWireGuard:
		if in.WireGuard == nil || r.User.WG == nil {
			return nil, errors.New("missing wireguard probe peer")
		}
		pub := r.WGPublicKey
		if pub == "" {
			var err error
			pub, err = singbox.WGPublicKey(in.WireGuard.PrivateKey)
			if err != nil {
				return nil, err
			}
		}
		cfg["endpoints"] = []any{map[string]any{"type": "wireguard", "tag": "probe", "system": false, "address": []string{r.User.WG.Address}, "private_key": r.User.WG.PrivateKey, "mtu": 1408, "peers": []any{map[string]any{"address": r.IP, "port": in.Port, "public_key": pub, "pre_shared_key": r.User.WG.PresharedKey, "allowed_ips": []string{"0.0.0.0/0", "::/0"}}}}}
	default:
		return nil, errors.New("unsupported UDP probe protocol")
	}
	return json.Marshal(cfg)
}
func (l *Local) Check(ctx context.Context, r Request) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if r.Inbound.Type == singbox.TypeVLESSReality {
		c, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(r.IP, strconv.Itoa(int(r.Inbound.Port))))
		if err == nil {
			_ = c.Close()
		}
		return err
	}
	u, err := url.Parse(l.ChallengeURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%w: HTTPS challenge URL is not configured", ErrUnavailable)
	}
	if l.Binary == "" {
		return fmt.Errorf("%w: sing-box binary is not configured", ErrUnavailable)
	}
	if err := l.ready(ctx); err != nil {
		return err
	}
	// Reserve a port only to choose one; readiness is established by the challenge response.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	cfg, err := ClientConfig(r, port)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "giocondo-vpn-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "config.json")
	if err = os.WriteFile(path, cfg, 0600); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, l.Binary, "run", "-c", path)
	// Never capture process output: configuration and errors can contain credentials.
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("%w: cannot start sing-box client", ErrUnavailable)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}()
	proxy, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if l.RootCAFile != "" {
		pem, err := os.ReadFile(l.RootCAFile)
		if err != nil {
			return err
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return errors.New("invalid challenge CA")
		}
		transport.TLSClientConfig.RootCAs = roots
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return err
	}
	nonce := hex.EncodeToString(b)
	q := u.Query()
	q.Set("challenge", nonce)
	u.RawQuery = q.Encode()
	for {
		req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		if err != nil {
			return err
		}
		res, err := client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(res.Body, 256))
			_ = res.Body.Close()
			if readErr == nil && res.StatusCode == 200 && string(body) == nonce {
				return nil
			}
		}
		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%s probe timed out", r.Inbound.Type)
		case <-done:
			timer.Stop()
			return errors.New("probe client exited before challenge succeeded")
		case <-timer.C:
		}
	}
}

// ChallengeHandler proves that traffic traversed the requested listener and tunnel.
func ChallengeHandler(w http.ResponseWriter, r *http.Request) {
	c := r.URL.Query().Get("challenge")
	b, err := hex.DecodeString(c)
	if err != nil || len(b) != 32 {
		http.Error(w, "invalid challenge", 400)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/plain")
	_, _ = io.WriteString(w, c)
}

// SafeError avoids persisting endpoint credentials or raw subprocess output.
func SafeError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	s := err.Error()
	if len(s) > 300 {
		s = s[:300]
	}
	return strings.ReplaceAll(s, "\n", " ")
}

func TargetKey(r Request) string {
	return net.JoinHostPort(r.IP, strconv.Itoa(int(r.Inbound.Port))) + "/" + string(r.Inbound.Type)
}

func (l *Local) ready(ctx context.Context) error {
	client, err := HTTPClient(l.RootCAFile)
	if err != nil {
		return fmt.Errorf("%w: invalid challenge CA", ErrUnavailable)
	}
	client.Timeout = 5 * time.Second
	defer client.CloseIdleConnections()
	challenge := make([]byte, 32)
	if _, err = rand.Read(challenge); err != nil {
		return err
	}
	nonce := hex.EncodeToString(challenge)
	u, err := url.Parse(l.ChallengeURL)
	if err != nil {
		return ErrUnavailable
	}
	q := u.Query()
	q.Set("challenge", nonce)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return ErrUnavailable
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: challenge endpoint unavailable", ErrUnavailable)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 256))
	if err != nil || res.StatusCode != 200 || string(data) != nonce {
		return fmt.Errorf("%w: invalid challenge response", ErrUnavailable)
	}
	return nil
}
