package engine

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
)

// Journal phases persisted in journal.json.
const (
	// journalPrepared is no longer written; it is recovered like journalActivating because agents
	// before the single activation phase may have left it.
	journalPrepared   = "prepared"
	journalActivating = "activating"
	journalCommitted  = "committed"
	journalRestoring  = "restoring"
)

const journalFile = "journal.json"

var errInvalidJournal = errors.New("invalid engine journal")

// journal is the durable apply record of one engine. Installed agents and the post-upgrade
// readiness check read it, so the JSON field names are a compatibility contract.
type journal struct {
	ID           string `json:"id"`
	Phase        string `json:"phase"`
	AppliedHash  string `json:"applied_hash"`
	TargetHash   string `json:"target_hash,omitempty"`
	HadOld       bool   `json:"had_old,omitempty"`
	RejectedHash string `json:"rejected_hash,omitempty"`
	Error        string `json:"error,omitempty"`
}

func (r *Runtime) journalPath() string { return filepath.Join(r.stateDir, journalFile) }

// readJournal returns the persisted journal; found is false when none has been written yet.
func (r *Runtime) readJournal() (j journal, found bool, err error) {
	b, err := r.store.ReadFile(r.journalPath())
	if errors.Is(err, fs.ErrNotExist) {
		return journal{}, false, nil
	}

	if err != nil {
		return journal{}, false, err
	}

	if err := json.Unmarshal(b, &j); err != nil {
		return journal{}, true, errInvalidJournal
	}

	return j, true, nil
}

// persist durably writes j and makes it the runtime's current journal.
func (r *Runtime) persist(j journal) error {
	b, err := json.Marshal(j)
	if err != nil {
		return err
	}

	if err := r.store.AtomicWrite(r.journalPath(), b); err != nil {
		return err
	}

	r.j = j
	if r.checkpoint != nil {
		r.checkpoint(j.Phase)
	}

	return nil
}
