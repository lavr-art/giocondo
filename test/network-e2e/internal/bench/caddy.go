package bench

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// CaddyCheck exercises the generated proxy with a local certificate instead of ACME.
func CaddyCheck(ctx context.Context, out io.Writer) error {
	source, err := os.ReadFile("/src/install.sh")
	if err != nil {
		return err
	}
	index := strings.LastIndex(string(source), `main "$@"`)
	if index < 0 {
		return errors.New("installer entry point not found")
	}
	for _, tc := range []struct{ mode, host string }{{"domain", "localhost"}, {"ip", "192.0.2.10"}} {
		if err := checkCaddy(ctx, string(source[:index]), tc.mode, tc.host); err != nil {
			return fmt.Errorf("generated %s Caddy HTTPS proxy: %w", tc.mode, err)
		}
		if _, err := fmt.Fprintf(out, "PASS generated %s Caddy HTTPS proxy with verified local TLS\n", tc.mode); err != nil {
			return err
		}
	}
	return nil
}

func checkCaddy(ctx context.Context, library, mode, host string) error {
	ip := "127.0.0.1"
	if mode == "ip" {
		ip = host
	}
	site, err := commandOutput(ctx, "sh", "-eu", "-c", library+"\nMODE="+mode+" DOMAIN=localhost IP="+ip+"\ncaddy_site\n")
	if err != nil {
		return err
	}
	if mode == "ip" {
		if !strings.Contains(string(site), "profile shortlived") {
			return errors.New("IP site does not request the shortlived ACME profile")
		}
		issuer := regexp.MustCompile(`(?s)\ttls \{.*?\n\t\}\n`)
		if len(issuer.FindAll(site, -1)) != 1 {
			return errors.New("expected exactly one IP ACME issuer block")
		}
		site = issuer.ReplaceAll(site, nil)
	}
	root, err := os.MkdirTemp("", "installer-caddy-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(root) }()
	cert, key := filepath.Join(root, "cert.pem"), filepath.Join(root, "key.pem")
	if _, err := commandOutput(ctx, "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
		"-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,IP:"+ip, "-keyout", key, "-out", cert); err != nil {
		return err
	}
	fixture := strings.Replace(string(site), "{\n", "{\n admin off\n auto_https disable_redirects\n", 1)
	end := strings.LastIndex(fixture, "}")
	if end < 0 {
		return errors.New("generated Caddy site is incomplete")
	}
	fixture = fixture[:end] + " tls " + cert + " " + key + "\n}\n"
	config := filepath.Join(root, "Caddyfile")
	if err := os.WriteFile(config, []byte(fixture), 0600); err != nil {
		return err
	}
	if _, err := commandOutput(ctx, "caddy", "validate", "--config", config, "--adapter", "caddyfile"); err != nil {
		return err
	}
	log, err := os.Create(filepath.Join(root, "caddy.log"))
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	processCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	process := exec.CommandContext(processCtx, "caddy", "run", "--config", config, "--adapter", "caddyfile")
	process.Stdout, process.Stderr = log, log
	process.WaitDelay = 5 * time.Second
	if err := process.Start(); err != nil {
		return err
	}
	defer func() {
		cancel()
		_ = process.Wait()
	}()
	curl := func(path string) (string, error) {
		requestCtx, requestCancel := context.WithTimeout(ctx, 3*time.Second)
		defer requestCancel()
		data, err := commandOutput(requestCtx, "curl", "-sS", "--max-time", "2", "--cacert", cert,
			"--connect-to", host+":8443:127.0.0.1:8443", "-w", "\n%{http_code}", "https://"+host+":8443"+path)
		return string(data), err
	}
	expected := strings.Repeat("d", 64)
	var lastErr error
	ready := false
	for range 20 {
		response, err := curl("/health/challenge?challenge=" + expected)
		if err == nil && response == expected+"\n200" {
			ready = true
			break
		}
		lastErr = err
		if err == nil {
			lastErr = fmt.Errorf("unexpected challenge response %q", response)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if !ready {
		logData, _ := os.ReadFile(log.Name())
		return fmt.Errorf("TLS readiness failed: %w\n%s", lastErr, logData)
	}
	admin, err := curl("/")
	if err != nil {
		return err
	}
	if mode == "ip" && !strings.HasSuffix(admin, "\n404") {
		return errors.New("IP site exposes the admin UI")
	}
	return nil
}
