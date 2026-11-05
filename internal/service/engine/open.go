package engine

import (
	"context"
	"errors"
	"io/fs"
)

// Open restores the runtime from its journal and must complete before any other method runs.
//
//   - No journal: the existing config is adopted as applied without touching the service.
//   - Committed journal: the active config must still have the confirmed hash.
//   - Interrupted apply: the previous config is restored, or the service is stopped if there was none.
//
// Any failure blocks the runtime, which then refuses applies; the state keeps reporting the
// failure and the current service activity.
func (r *Runtime) Open(ctx context.Context) error {
	if r.store == nil {
		return errors.New("engine store is required")
	}

	r.op.Lock()
	defer r.op.Unlock()

	r.setState(func(s *State) { *s = State{ID: r.driver.ID(), Phase: phaseReady} })
	defer r.refresh(ctx)

	if err := r.recover(ctx); err != nil {
		return r.block(err)
	}

	r.readVersion(ctx)

	return nil
}

func (r *Runtime) recover(ctx context.Context) error {
	j, found, err := r.readJournal()
	if err != nil {
		return err
	}

	if !found {
		return r.adopt()
	}

	if j.ID != r.driver.ID() {
		return errors.New("engine journal identity mismatch")
	}

	r.j = j
	r.setState(func(s *State) {
		s.AppliedHash = j.AppliedHash
		s.RejectedHash = j.RejectedHash
		s.Error = j.Error
	})

	switch j.Phase {
	case journalCommitted:
		return r.verifyCommitted()
	case journalPrepared, journalActivating, journalRestoring:
		return r.restore(ctx, "interrupted apply recovered")
	default:
		return errors.New("unknown engine journal phase")
	}
}

// adopt takes over an installation that predates the journal without restarting its service.
func (r *Runtime) adopt() error {
	data, found, err := r.readConfig()
	if err != nil {
		return err
	}

	r.j = journal{ID: r.driver.ID(), Phase: journalCommitted, AppliedHash: configHash(data, found)}
	r.setState(func(s *State) { s.AppliedHash = r.j.AppliedHash })

	return nil
}

// verifyCommitted checks that the active config is the committed one and re-adopts a config
// changed outside the agent.
func (r *Runtime) verifyCommitted() error {
	data, found, err := r.readConfig()
	if err != nil {
		return err
	}

	if configHash(data, found) == r.j.AppliedHash {
		return nil
	}

	return r.readopt(data, found)
}

// readopt durably records a config changed outside the agent as the applied one without restarting
// the engine, and reports the change. The panel is the source of truth: it sees a differing applied
// hash and sends its config again, and until that config is committed the external one stays the
// rollback target. Blocking instead would leave the node unmanageable without manual repair.
func (r *Runtime) readopt(data []byte, found bool) error {
	j := journal{
		ID:           r.driver.ID(),
		Phase:        journalCommitted,
		AppliedHash:  configHash(data, found),
		RejectedHash: r.j.RejectedHash,
		Error:        errChangedOutside.Error(),
	}
	if err := r.persist(j); err != nil {
		return err
	}

	r.setState(func(s *State) { s.AppliedHash = j.AppliedHash; s.Error = j.Error })

	return nil
}

// readConfig reads the active config; found is false when there is none.
func (r *Runtime) readConfig() (data []byte, found bool, err error) {
	data, err = r.store.ReadFile(r.configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, err
	}

	return data, true, nil
}

// configHash returns the hash recorded for the active config, empty when there is none.
func configHash(data []byte, found bool) string {
	if !found {
		return ""
	}

	return Hash(data)
}

// readVersion is best effort: an unknown version is reported empty and does not block the engine.
func (r *Runtime) readVersion(ctx context.Context) {
	c, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()

	if v, err := r.driver.Version(c); err == nil {
		r.setState(func(s *State) { s.Version = v })
	}
}

// restore returns the engine to the last confirmed config recorded in r.j and records the target
// of the interrupted apply as rejected. It is repeatable: the restoring phase is persisted first,
// so a crash during restore is recovered by the next Open.
func (r *Runtime) restore(ctx context.Context, reason string) error {
	j := r.j

	j.Phase = journalRestoring
	if err := r.persist(j); err != nil {
		return err
	}

	if j.HadOld {
		if err := r.reinstatePrevious(ctx, j.AppliedHash); err != nil {
			return err
		}
	} else if err := r.removeFailedFirst(ctx); err != nil {
		return err
	}

	j = journal{
		ID:           r.driver.ID(),
		Phase:        journalCommitted,
		AppliedHash:  j.AppliedHash,
		RejectedHash: j.TargetHash,
		Error:        reason,
	}
	if err := r.persist(j); err != nil {
		return err
	}

	r.setState(func(s *State) {
		s.Phase = phaseReady
		s.AppliedHash = j.AppliedHash
		s.RejectedHash = j.RejectedHash
		s.Error = reason
	})

	return nil
}

// reinstatePrevious puts the .prev backup back and restarts the engine with it.
func (r *Runtime) reinstatePrevious(ctx context.Context, appliedHash string) error {
	old, err := r.store.ReadFile(r.backupPath())
	if err != nil {
		return err
	}

	if Hash(old) != appliedHash {
		return errors.New("rollback config hash mismatch")
	}

	if err := r.store.AtomicWrite(r.configPath, old); err != nil {
		return err
	}

	return r.restart(ctx)
}

// removeFailedFirst handles a failed first config: there is nothing to roll back to, so the
// engine is stopped and the config removed.
func (r *Runtime) removeFailedFirst(ctx context.Context) error {
	if err := r.stop(ctx); err != nil {
		return err
	}

	if err := r.store.RemoveFile(r.configPath); err != nil {
		return err
	}

	r.refresh(ctx)

	if r.Snapshot().Active {
		return errors.New("engine remains active after stop")
	}

	return nil
}
