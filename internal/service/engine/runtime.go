package engine

import (
	"context"
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
)

// Runtime phases reported in State. The panel reads only the blocked phase.
const (
	phaseReady    = "ready"
	phaseApplying = "applying"
	phaseBlocked  = nodesync.EnginePhaseBlocked
)

// Bounds for external engine operations. Rollback gets its own budget because it runs even after the
// apply context was cancelled and covers a restart, the settle delay and an activity check.
const (
	versionTimeout  = 5 * time.Second
	statusTimeout   = 5 * time.Second
	collectTimeout  = 10 * time.Second
	validateTimeout = 30 * time.Second
	commandTimeout  = 30 * time.Second
	rollbackTimeout = 75 * time.Second
)

func (r *Runtime) setState(fn func(*State)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	fn(&r.state)
}

// block marks the runtime as requiring recovery: Apply is refused until a later Open succeeds.
func (r *Runtime) block(err error) error {
	r.setState(func(s *State) { s.Phase = phaseBlocked; s.Error = err.Error() })

	return err
}

// restart collects counters, restarts the service and checks that it is still active after SettleDelay.
func (r *Runtime) restart(ctx context.Context) error {
	r.collect(ctx)

	c, cancel := context.WithTimeout(ctx, commandTimeout)
	err := r.driver.Restart(c)

	cancel()

	if err != nil {
		r.refresh(ctx)

		return errors.New("engine restart failed")
	}

	timer := time.NewTimer(r.settleDelay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}

	r.refresh(ctx)

	if !r.Snapshot().Active {
		return errors.New("engine is not running after restart")
	}

	return nil
}

// stop collects counters, stops the service and checks that it is no longer active.
func (r *Runtime) stop(ctx context.Context) error {
	r.collect(ctx)

	c, cancel := context.WithTimeout(ctx, commandTimeout)
	err := r.driver.Stop(c)

	cancel()

	if err != nil {
		return errors.New("cannot stop failed engine")
	}

	return nil
}
