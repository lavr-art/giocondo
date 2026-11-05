// Package protocheck probes the public protocol listeners, independently of agent Sync.
package protocheck

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
	"time"

	"github.com/artemlavr/giocondo/internal/clients/probepoint"
	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// Local is the local health checker.
type Local struct {
	Binary       string
	ChallengeURL string
	RootCAFile   string
}

// ClientConfig routes a temporary HTTP proxy entirely through the requested protocol.
//
//nolint:goconst // sing-box JSON keys read better inline.
func ClientConfig(r probe.Request, port int) ([]byte, error) {
	in := r.Inbound
	cfg := map[string]any{
		"log":      map[string]any{"disabled": true},
		"inbounds": []any{map[string]any{"type": "http", "listen": "127.0.0.1", "listen_port": port}},
		"route":    map[string]any{"final": "probe", "default_domain_resolver": "local"},
		"dns":      map[string]any{"servers": []any{map[string]any{"type": "local", "tag": "local"}}},
	}

	switch in.Type {
	case protocol.TypeHysteria2:
		if in.Hysteria2 == nil {
			return nil, errors.New("missing hysteria2 parameters")
		}

		tls := map[string]any{"enabled": true, "server_name": r.Host}
		if in.Hysteria2.TLSMode() == protocol.TLSSelfSigned {
			// Trust exactly the node's existing certificate (no insecure UDP probe).
			tls["certificate"] = []string{in.Hysteria2.CertPEM}
		}

		out := map[string]any{
			"type":        "hysteria2",
			"tag":         "probe",
			"server":      r.IP,
			"server_port": in.Port,
			"password":    r.User.UUID,
			"tls":         tls,
		}
		if in.Hysteria2.ObfsPassword != "" {
			out["obfs"] = map[string]any{"type": "salamander", "password": in.Hysteria2.ObfsPassword}
		}

		cfg["outbounds"] = []any{out}
	case protocol.TypeWireGuard:
		if in.WireGuard == nil || r.User.WG == nil {
			return nil, errors.New("missing wireguard probe peer")
		}

		pub := r.WGPublicKey
		if pub == "" {
			var err error

			pub, err = protocol.WGPublicKey(in.WireGuard.PrivateKey)
			if err != nil {
				return nil, err
			}
		}

		cfg["endpoints"] = []any{
			map[string]any{
				"type":        "wireguard",
				"tag":         "probe",
				"system":      false,
				"address":     []string{r.User.WG.Address},
				"private_key": r.User.WG.PrivateKey,
				"mtu":         1408,
				"peers": []any{
					map[string]any{
						"address":        r.IP,
						"port":           in.Port,
						"public_key":     pub,
						"pre_shared_key": r.User.WG.PresharedKey,
						"allowed_ips":    []string{"0.0.0.0/0", "::/0"},
					},
				},
			},
		}
	case protocol.TypeVLESSReality:
		// Reality is a TCP protocol and is probed by dialing the inbound directly.
		return nil, errors.New("vless reality is not an HTTP proxy protocol")
	default:
		return nil, errors.New("unsupported UDP probe protocol")
	}

	return json.Marshal(cfg)
}

// Check performs the check.
func (l *Local) Check(ctx context.Context, r probe.Request) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if r.Inbound.Type == protocol.TypeVLESSReality {
		c, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(r.IP, strconv.Itoa(int(r.Inbound.Port))))
		if err == nil {
			_ = c.Close()
		}

		return err
	}

	u, err := url.Parse(l.ChallengeURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%w: HTTPS challenge URL is not configured", probe.ErrUnavailable)
	}

	if l.Binary == "" {
		return fmt.Errorf("%w: sing-box binary is not configured", probe.ErrUnavailable)
	}

	if err := l.ready(ctx); err != nil {
		return err
	}

	port, err := freePort(ctx)
	if err != nil {
		return err
	}

	cfg, err := ClientConfig(r, port)
	if err != nil {
		return err
	}

	exited, stop, err := l.startClient(ctx, cfg)
	if err != nil {
		return err
	}

	defer stop()

	client, err := l.proxyClient(port)
	if err != nil {
		return err
	}

	defer client.CloseIdleConnections()

	return challenge(ctx, client, u, exited, r.Inbound.Type)
}

// freePort reserves a port only to choose one; readiness is established by the challenge response.
func freePort(ctx context.Context) (int, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}

	addr, ok := listener.Addr().(*net.TCPAddr)
	_ = listener.Close()

	if !ok {
		return 0, fmt.Errorf("unexpected listener address %T", listener.Addr())
	}

	return addr.Port, nil
}

// startClient runs a sing-box client with cfg. exited closes when the process ends;
// stop kills it and removes its configuration.
func (l *Local) startClient(ctx context.Context, cfg []byte) (exited <-chan struct{}, stop func(), err error) {
	dir, err := os.MkdirTemp("", "giocondo-vpn-probe-")
	if err != nil {
		return nil, nil, err
	}

	removeDir := func() { _ = os.RemoveAll(dir) }

	path := filepath.Join(dir, "config.json")
	if err = os.WriteFile(path, cfg, 0600); err != nil {
		removeDir()

		return nil, nil, err
	}

	//nolint:gosec // the binary path comes from trusted panel configuration; the arguments are fixed.
	cmd := exec.CommandContext(ctx, l.Binary, "run", "-c", path)
	// Never capture process output: configuration and errors can contain credentials.
	if err = cmd.Start(); err != nil {
		removeDir()

		return nil, nil, fmt.Errorf("%w: cannot start sing-box client", probe.ErrUnavailable)
	}

	done := make(chan struct{})

	go func() { _ = cmd.Wait(); close(done) }()

	return done, func() {
		_ = cmd.Process.Kill()

		select {
		case <-done:
		case <-time.After(time.Second):
		}

		removeDir()
	}, nil
}

// proxyClient sends requests through the local client proxy and trusts the configured challenge CA.
func (l *Local) proxyClient(port int) (*http.Client, error) {
	proxy, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	transport := &http.Transport{Proxy: http.ProxyURL(proxy), TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}

	if l.RootCAFile != "" {
		pem, err := os.ReadFile(l.RootCAFile)
		if err != nil {
			return nil, err
		}

		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}

		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("invalid challenge CA")
		}

		transport.TLSClientConfig.RootCAs = roots
	}

	return &http.Client{
		Transport:     transport,
		Timeout:       3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// challenge polls the challenge URL with a fresh nonce until the panel echoes it through the proxy.
func challenge(
	ctx context.Context,
	client *http.Client,
	u *url.URL,
	exited <-chan struct{},
	typ protocol.InboundType,
) error {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return err
	}

	nonce := hex.EncodeToString(b)
	q := u.Query()
	q.Set("challenge", nonce)
	u.RawQuery = q.Encode()

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return err
		}

		res, err := client.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(res.Body, 256))

			_ = res.Body.Close()
			if readErr == nil && res.StatusCode == http.StatusOK && string(body) == nonce {
				return nil
			}
		}

		timer := time.NewTimer(150 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()

			return fmt.Errorf("%s probe timed out", typ)
		case <-exited:
			timer.Stop()

			return errors.New("probe client exited before challenge succeeded")
		case <-timer.C:
		}
	}
}

func (l *Local) ready(ctx context.Context) error {
	client, err := probepoint.HTTPClient(l.RootCAFile)
	if err != nil {
		return fmt.Errorf("%w: invalid challenge CA", probe.ErrUnavailable)
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
		return probe.ErrUnavailable
	}

	q := u.Query()
	q.Set("challenge", nonce)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return probe.ErrUnavailable
	}

	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: challenge endpoint unavailable", probe.ErrUnavailable)
	}

	defer func() { _ = res.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(res.Body, 256))
	if err != nil || res.StatusCode != http.StatusOK || string(data) != nonce {
		return fmt.Errorf("%w: invalid challenge response", probe.ErrUnavailable)
	}

	return nil
}
