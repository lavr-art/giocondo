package agent

import (
	"context"
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/platform/servicecheck"
)

const checkTimeout = 5 * time.Second

// Check verifies, without changing state, that the agent running as pid has a usable local engine:
// the active config passes validation, the service is active and the last apply was committed.
// It reads the agent's effective environment from /proc and does not contact the panel.
func Check(pid int) error {
	getenv, err := servicecheck.Environment(pid)
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(getenv)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	driver := singBoxDriver(cfg, nil)
	if err := driver.Validate(ctx, cfg.ConfigPath); err != nil {
		return err
	}

	if active, err := driver.Active(ctx); err != nil || !active {
		return errors.New("sing-box is not active")
	}

	return singBoxRuntime(cfg, driver, nil).Inspect()
}
