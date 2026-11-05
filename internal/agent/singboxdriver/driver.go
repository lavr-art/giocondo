// Package singboxdriver adapts the installed sing-box binary and its loopback stats API.
package singboxdriver

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/artemlavr/giocondo/internal/agent/engine"
)

type Driver struct {
	Binary                         string
	RestartCmd, StatusCmd, StopCmd []string
	Collector                      *Collector
}

func (d *Driver) ID() string { return "sing-box" }
func (d *Driver) Validate(ctx context.Context, path string) error {
	_, e := run(ctx, d.Binary, "check", "-c", path)
	return e
}
func (d *Driver) Version(ctx context.Context) (string, error) {
	s, e := run(ctx, d.Binary, "version")
	line, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(line), e
}
func (d *Driver) Restart(ctx context.Context) error { return command(ctx, d.RestartCmd) }
func (d *Driver) Stop(ctx context.Context) error    { return command(ctx, d.StopCmd) }
func (d *Driver) Active(ctx context.Context) (bool, error) {
	e := command(ctx, d.StatusCmd)
	if e == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(e, &exit) && ctx.Err() == nil {
		return false, nil
	}
	return false, e
}
func (d *Driver) Collect(ctx context.Context) (map[string]engine.Traffic, error) {
	if d.Collector == nil {
		return nil, nil
	}
	return d.Collector.Collect(ctx)
}
func command(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("empty engine command")
	}
	_, e := run(ctx, args[0], args[1:]...)
	return e
}

// Validator output can contain credentials; never include subprocess output in errors.
func run(ctx context.Context, name string, args ...string) (string, error) {
	out, e := exec.CommandContext(ctx, name, args...).Output()
	return string(out), e
}
