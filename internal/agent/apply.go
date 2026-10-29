// Package agent is the node agent logic: it applies the sing-box config, collects statistics and
// holds the Sync stream with the panel. It imports only nodeapi, stdlib and gRPC (see cmd/agent/deps_test.go).
package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Applier writes the sing-box config and restarts the service, rolling back to the previous config on failure.
type Applier struct {
	SingBoxBin    string                    // path to sing-box for `sing-box check`
	ConfigPath    string                    // /etc/giocondo-vpn/sing-box/config.json
	RestartCmd    []string                  // e.g. systemctl restart sing-box
	StatusCmd     []string                  // exit code 0 means the service is running
	SettleDelay   time.Duration             // how long to wait after a restart before checking the status
	BeforeRestart func(ctx context.Context) // called before a restart (statistics collection)
}

// Hash is the hex sha256, computed the same way by the panel.
func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// CurrentHash returns the hash of the current config or "" if there is none.
func (a *Applier) CurrentHash() string {
	data, err := os.ReadFile(a.ConfigPath)
	if err != nil {
		return ""
	}
	return Hash(data)
}

func (a *Applier) prevPath() string { return a.ConfigPath + ".prev" }

// Apply applies a config. If the config fails `sing-box check`, nothing changes.
// If the service is not running after the restart, the previous config is restored and the service
// is restarted again. The error describes what happened.
func (a *Applier) Apply(ctx context.Context, data []byte) error {
	dir := filepath.Dir(a.ConfigPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := writeTemp(dir, filepath.Base(a.ConfigPath)+".new", data)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()

	if out, err := run(ctx, a.SingBoxBin, "check", "-c", tmp); err != nil {
		return fmt.Errorf("sing-box check: %w: %s", err, out)
	}

	old, err := os.ReadFile(a.ConfigPath)
	hadOld := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if hadOld {
		if err := os.WriteFile(a.prevPath(), old, 0o600); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
	}
	if err := os.Rename(tmp, a.ConfigPath); err != nil {
		return err
	}

	restartErr := a.restartAndCheck(ctx)
	if restartErr == nil {
		return nil
	}
	if !hadOld {
		return fmt.Errorf("new config failed and there is no previous one: %w", restartErr)
	}
	if err := os.WriteFile(a.ConfigPath, old, 0o600); err != nil {
		return fmt.Errorf("%w; rollback write failed: %w", restartErr, err)
	}
	if err := a.restartAndCheck(ctx); err != nil {
		return fmt.Errorf("%w; rollback restart failed too: %w", restartErr, err)
	}
	return fmt.Errorf("rolled back to previous config: %w", restartErr)
}

func (a *Applier) restartAndCheck(ctx context.Context) error {
	if a.BeforeRestart != nil {
		a.BeforeRestart(ctx)
	}
	if out, err := runCmd(ctx, a.RestartCmd); err != nil {
		return fmt.Errorf("restart: %w: %s", err, out)
	}
	select {
	case <-time.After(a.SettleDelay):
	case <-ctx.Done():
		return ctx.Err()
	}
	if !a.Active(ctx) {
		return errors.New("sing-box is not running after restart")
	}
	return nil
}

// Active reports whether sing-box is running (via StatusCmd).
func (a *Applier) Active(ctx context.Context) bool {
	_, err := runCmd(ctx, a.StatusCmd)
	return err == nil
}

// Version returns the first line of `sing-box version`.
func (a *Applier) Version(ctx context.Context) string {
	out, err := run(ctx, a.SingBoxBin, "version")
	if err != nil {
		return ""
	}
	line, _, _ := strings.Cut(out, "\n")
	return strings.TrimSpace(line)
}

func writeTemp(dir, name string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func runCmd(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("empty command")
	}
	return run(ctx, argv[0], argv[1:]...)
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
