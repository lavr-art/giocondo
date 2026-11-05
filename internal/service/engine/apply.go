package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

var (
	errValidation     = errors.New("engine config validation failed")
	errChangedOutside = errors.New("config changed outside agent")
)

func (r *Runtime) candidatePath() string { return r.configPath + ".new" }
func (r *Runtime) backupPath() string    { return r.configPath + ".prev" }

// Apply makes data, whose SHA-256 is hash, the active engine config. Applying the confirmed hash
// again is a no-op. Each failure has one of three outcomes:
//
//   - rejected: the service was not touched; hash is recorded as rejected and later applies are allowed;
//   - rolled back: the new config failed after activation and the previous one is running again;
//   - blocked: the durable state is uncertain; applies are refused until Open recovers.
//
// Rollback runs with its own bounded timeout even if ctx is cancelled.
func (r *Runtime) Apply(ctx context.Context, data []byte, hash string) error {
	if r.store == nil {
		return errors.New("engine store is required")
	}

	r.op.Lock()
	defer r.op.Unlock()

	switch state := r.Snapshot(); {
	case state.Phase == phaseBlocked:
		return errors.New("engine blocked: recovery required")
	case Hash(data) != hash:
		return errors.New("config hash mismatch")
	case state.AppliedHash == hash:
		return nil
	}

	staged, err := r.stageCandidate(ctx, data)
	if staged {
		defer func() { _ = r.store.RemoveFile(r.candidatePath()) }()
	}

	if err != nil {
		return r.reject(hash, err)
	}

	hadOld, err := r.backupCurrent()
	if err != nil {
		return r.reject(hash, err)
	}

	if err := r.beginActivation(hash, hadOld); err != nil {
		return r.block(err)
	}

	if err := r.activate(ctx, data); err != nil {
		return r.rollback(ctx, err)
	}

	if err := r.commit(hash); err != nil {
		return r.block(err)
	}

	return nil
}

// stageCandidate writes data next to the active config and validates it there.
// staged reports whether the candidate file exists and must be removed.
func (r *Runtime) stageCandidate(ctx context.Context, data []byte) (staged bool, err error) {
	if err := r.store.MkdirDurable(filepath.Dir(r.configPath)); err != nil {
		return false, err
	}

	if err := r.store.AtomicWrite(r.candidatePath(), data); err != nil {
		return false, err
	}

	c, cancel := context.WithTimeout(ctx, validateTimeout)
	defer cancel()

	// The validator error can contain config contents, so only a generic error is reported.
	if err := r.driver.Validate(c, r.candidatePath()); err != nil {
		return true, errValidation
	}

	return true, nil
}

// backupCurrent saves the running config as .prev for rollback. A config changed outside the agent
// is re-adopted first, so the rollback target is always the config the engine actually runs.
func (r *Runtime) backupCurrent() (hadOld bool, err error) {
	old, hadOld, err := r.readConfig()
	if err != nil {
		return false, err
	}

	if configHash(old, hadOld) != r.Snapshot().AppliedHash {
		if err := r.readopt(old, hadOld); err != nil {
			return false, err
		}
	}

	if hadOld {
		if err := r.store.AtomicWrite(r.backupPath(), old); err != nil {
			return false, err
		}
	}

	return hadOld, nil
}

// beginActivation durably records the intent to replace the config, so a crash from here on is
// rolled back by Open.
func (r *Runtime) beginActivation(hash string, hadOld bool) error {
	j := journal{
		ID:          r.driver.ID(),
		Phase:       journalActivating,
		AppliedHash: r.Snapshot().AppliedHash,
		TargetHash:  hash,
		HadOld:      hadOld,
	}
	if err := r.persist(j); err != nil {
		return err
	}

	r.setState(func(s *State) { s.Phase = phaseApplying })

	return nil
}

// activate replaces the active config and restarts the engine with it.
func (r *Runtime) activate(ctx context.Context, data []byte) error {
	if err := r.store.AtomicWrite(r.configPath, data); err != nil {
		return err
	}

	if r.checkpoint != nil {
		r.checkpoint("replaced")
	}

	return r.restart(ctx)
}

// commit confirms hash as applied once the engine runs with it.
func (r *Runtime) commit(hash string) error {
	if r.checkpoint != nil {
		r.checkpoint("started")
	}

	if err := r.persist(journal{ID: r.driver.ID(), Phase: journalCommitted, AppliedHash: hash}); err != nil {
		return err
	}

	r.setState(func(s *State) { s.Phase = phaseReady; s.AppliedHash = hash; s.RejectedHash = ""; s.Error = "" })

	return nil
}

// rollback restores the previous config after a failed activation, under a bounded context that
// survives cancellation of ctx: the agent must not leave a half-applied engine while it is alive.
func (r *Runtime) rollback(ctx context.Context, cause error) error {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()

	if err := r.restore(c, cause.Error()); err != nil {
		return r.block(fmt.Errorf("apply failed; rollback failed: %w", err))
	}

	return fmt.Errorf("rolled back: %w", cause)
}

// reject records hash as rejected without touching the service and keeps the runtime ready.
func (r *Runtime) reject(hash string, cause error) error {
	j := r.j
	j.RejectedHash = hash
	j.Error = cause.Error()

	if err := r.persist(j); err != nil {
		return r.block(fmt.Errorf("record rejection: %w", err))
	}

	r.setState(func(s *State) { s.Phase = phaseReady; s.RejectedHash = hash; s.Error = cause.Error() })

	return cause
}
