package engine

import "errors"

// Inspect reports, without changing files, state or the service, whether the engine finished its
// last apply: the journal belongs to this engine, is committed, and the active config has the
// confirmed hash. A missing journal passes, since agents before the runtime journal ran without one.
func (r *Runtime) Inspect() error {
	j, found, err := r.readJournal()
	if err != nil || !found {
		return err
	}

	data, err := r.store.ReadFile(r.configPath)
	if err != nil {
		return err
	}

	if j.ID != r.driver.ID() || j.Phase != journalCommitted || j.AppliedHash != Hash(data) {
		return errors.New("engine journal is not committed to the active configuration")
	}

	return nil
}
