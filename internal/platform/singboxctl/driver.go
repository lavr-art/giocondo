// Package singboxctl controls the installed sing-box service: it validates configs with the
// binary, restarts and stops the systemd unit and reads traffic counters over v2ray_api.
// It implements engine.Driver and never writes managed configuration files itself.
package singboxctl

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
	"github.com/artemlavr/giocondo/internal/transport/grpc/v2raystats"
)

// Driver runs sing-box commands. Commands are argv lists executed without a shell.
type Driver struct {
	Binary                         string
	RestartCmd, StatusCmd, StopCmd []string
	// Stats reads and resets traffic counters; nil disables collection.
	Stats v2raystats.StatsServiceClient
}

// ID returns the engine identifier used in the state directory, journal and Sync reports.
func (d *Driver) ID() string { return nodesync.SingBoxEngineID }

// Validate runs "sing-box check" against the config at path.
func (d *Driver) Validate(ctx context.Context, path string) error {
	_, e := run(ctx, d.Binary, "check", "-c", path)

	return e
}

// Version returns the first line of "sing-box version".
func (d *Driver) Version(ctx context.Context) (string, error) {
	s, e := run(ctx, d.Binary, "version")
	line, _, _ := strings.Cut(s, "\n")

	return strings.TrimSpace(line), e
}

// Restart restarts the sing-box service.
func (d *Driver) Restart(ctx context.Context) error { return command(ctx, d.RestartCmd) }

// Stop stops the sing-box service.
func (d *Driver) Stop(ctx context.Context) error { return command(ctx, d.StopCmd) }

// Active reports whether the service is running. A non-zero exit of the status command means
// inactive; any other failure, including cancellation, is returned as an error.
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
