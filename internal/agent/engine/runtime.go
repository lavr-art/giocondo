package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type journal struct {
	ID           string `json:"id"`
	Phase        string `json:"phase"`
	AppliedHash  string `json:"applied_hash"`
	TargetHash   string `json:"target_hash,omitempty"`
	HadOld       bool   `json:"had_old,omitempty"`
	RejectedHash string `json:"rejected_hash,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Runtime serializes configuration and destructive counter reads for one engine.
// Snapshot is independent of external calls, so a slow engine cannot stall Sync.
type Runtime struct {
	Driver               Driver
	ConfigPath, StateDir string
	SettleDelay          time.Duration
	OnTraffic            func(map[string]Traffic)
	op                   sync.Mutex
	mu                   sync.RWMutex
	state                State
	j                    journal
	// checkpoint is used by subprocess tests to interrupt actual durable phases.
	checkpoint func(string)
}

func (r *Runtime) Snapshot() State          { r.mu.RLock(); defer r.mu.RUnlock(); return r.state }
func (r *Runtime) setState(fn func(*State)) { r.mu.Lock(); defer r.mu.Unlock(); fn(&r.state) }
func (r *Runtime) persist(j journal) error {
	b, e := json.Marshal(j)
	if e != nil {
		return e
	}
	if e = AtomicWrite(filepath.Join(r.StateDir, "journal.json"), b); e != nil {
		return e
	}
	r.j = j
	if r.checkpoint != nil {
		r.checkpoint(j.Phase)
	}
	return nil
}
func (r *Runtime) Open(ctx context.Context) error {
	r.op.Lock()
	defer r.op.Unlock()
	r.setState(func(s *State) { *s = State{ID: r.Driver.ID(), Phase: "ready"} })
	defer r.refresh(ctx)
	b, e := os.ReadFile(filepath.Join(r.StateDir, "journal.json"))
	if errors.Is(e, os.ErrNotExist) {
		data, err := os.ReadFile(r.ConfigPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return r.block(err)
		}
		r.j = journal{ID: r.Driver.ID(), Phase: "committed"}
		if err == nil {
			r.j.AppliedHash = Hash(data)
		}
		// Adopt existing installations without changing or restarting their service.
		r.setState(func(s *State) { s.AppliedHash = r.j.AppliedHash })
	} else if e != nil {
		return r.block(e)
	} else {
		if e = json.Unmarshal(b, &r.j); e != nil {
			return r.block(errors.New("invalid engine journal"))
		}
		if r.j.ID != r.Driver.ID() {
			return r.block(errors.New("engine journal identity mismatch"))
		}
		r.setState(func(s *State) {
			s.AppliedHash = r.j.AppliedHash
			s.RejectedHash = r.j.RejectedHash
			s.Error = r.j.Error
		})
		switch r.j.Phase {
		case "committed":
			data, err := os.ReadFile(r.ConfigPath)
			if r.j.AppliedHash == "" && errors.Is(err, os.ErrNotExist) {
				break
			}
			if err != nil || Hash(data) != r.j.AppliedHash {
				return r.block(errors.New("active config differs from committed journal"))
			}
		case "prepared", "activating", "restoring":
			if e = r.restore(ctx, "interrupted apply recovered"); e != nil {
				return r.block(e)
			}
		default:
			return r.block(errors.New("unknown engine journal phase"))
		}
	}
	vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	v, err := r.Driver.Version(vctx)
	cancel()
	if err == nil {
		r.setState(func(s *State) { s.Version = v })
	}
	return nil
}
func (r *Runtime) block(e error) error {
	r.setState(func(s *State) { s.Phase = "blocked"; s.Error = e.Error() })
	return e
}
func (r *Runtime) refresh(ctx context.Context) {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	active, e := r.Driver.Active(c)
	r.setState(func(s *State) {
		s.Active = active
		if e != nil && s.Error == "" {
			s.Error = "engine status check failed"
		} else if e == nil && s.Phase != "blocked" && s.Error == "engine status check failed" {
			s.Error = ""
		}
	})
}
func (r *Runtime) Refresh(ctx context.Context) {
	if !r.op.TryLock() {
		return
	}
	defer r.op.Unlock()
	r.refresh(ctx)
}
func (r *Runtime) collect(ctx context.Context) {
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	t, e := r.Driver.Collect(c)
	if e == nil && r.OnTraffic != nil {
		r.OnTraffic(t)
	}
}
func (r *Runtime) Collect(ctx context.Context) { r.op.Lock(); defer r.op.Unlock(); r.collect(ctx) }
func (r *Runtime) restart(ctx context.Context) error {
	r.collect(ctx)
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	e := r.Driver.Restart(c)
	cancel()
	if e != nil {
		r.refresh(ctx)
		return errors.New("engine restart failed")
	}
	timer := time.NewTimer(r.SettleDelay)
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
func (r *Runtime) reject(hash string, e error) error {
	j := r.j
	j.RejectedHash = hash
	j.Error = e.Error()
	if pe := r.persist(j); pe != nil {
		return r.block(fmt.Errorf("record rejection: %w", pe))
	}
	r.setState(func(s *State) { s.Phase = "ready"; s.RejectedHash = hash; s.Error = e.Error() })
	return e
}
func (r *Runtime) Apply(ctx context.Context, data []byte, hash string) error {
	r.op.Lock()
	defer r.op.Unlock()
	if r.Snapshot().Phase == "blocked" {
		return errors.New("engine blocked: recovery required")
	}
	if Hash(data) != hash {
		return errors.New("config hash mismatch")
	}
	if r.Snapshot().AppliedHash == hash {
		return nil
	}
	if e := mkdirDurable(filepath.Dir(r.ConfigPath)); e != nil {
		return r.reject(hash, e)
	}
	candidate := r.ConfigPath + ".new"
	if e := AtomicWrite(candidate, data); e != nil {
		return r.reject(hash, e)
	}
	defer func() { _ = removeFile(candidate) }()
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	e := r.Driver.Validate(c, candidate)
	cancel()
	if e != nil {
		return r.reject(hash, errors.New("engine config validation failed"))
	}
	old, e := os.ReadFile(r.ConfigPath)
	hadOld := e == nil
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return r.reject(hash, e)
	}
	if (!hadOld && r.Snapshot().AppliedHash != "") || (hadOld && Hash(old) != r.Snapshot().AppliedHash) {
		return r.block(errors.New("config changed outside agent"))
	}
	if hadOld {
		if e = AtomicWrite(r.ConfigPath+".prev", old); e != nil {
			return r.reject(hash, e)
		}
	}
	j := journal{ID: r.Driver.ID(), Phase: "prepared", AppliedHash: r.Snapshot().AppliedHash, TargetHash: hash, HadOld: hadOld}
	if e = r.persist(j); e != nil {
		return r.block(e)
	}
	r.setState(func(s *State) { s.Phase = "applying" })
	j.Phase = "activating"
	if e = r.persist(j); e != nil {
		return r.block(e)
	}
	if e = AtomicWrite(r.ConfigPath, data); e == nil {
		if r.checkpoint != nil {
			r.checkpoint("replaced")
		}
		e = r.restart(ctx)
	}
	if e != nil {
		cause := e
		// A cancelled operation still gets a bounded rollback while the process is alive.
		recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), 75*time.Second)
		defer cancel()
		if re := r.restore(recovery, cause.Error()); re != nil {
			return r.block(fmt.Errorf("apply failed; rollback failed: %w", re))
		}
		return fmt.Errorf("rolled back: %w", cause)
	}
	if r.checkpoint != nil {
		r.checkpoint("started")
	}
	j = journal{ID: r.Driver.ID(), Phase: "committed", AppliedHash: hash}
	if e = r.persist(j); e != nil {
		return r.block(e)
	}
	r.setState(func(s *State) { s.Phase = "ready"; s.AppliedHash = hash; s.RejectedHash = ""; s.Error = "" })
	return nil
}
func (r *Runtime) restore(ctx context.Context, reason string) error {
	j := r.j
	j.Phase = "restoring"
	if e := r.persist(j); e != nil {
		return e
	}
	if j.HadOld {
		old, e := os.ReadFile(r.ConfigPath + ".prev")
		if e != nil {
			return e
		}
		if Hash(old) != j.AppliedHash {
			return errors.New("rollback config hash mismatch")
		}
		if e = AtomicWrite(r.ConfigPath, old); e != nil {
			return e
		}
		if e = r.restart(ctx); e != nil {
			return e
		}
	} else {
		r.collect(ctx)
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		e := r.Driver.Stop(c)
		cancel()
		if e != nil {
			return errors.New("cannot stop failed engine")
		}
		if e = removeFile(r.ConfigPath); e != nil {
			return e
		}
		r.refresh(ctx)
		if r.Snapshot().Active {
			return errors.New("engine remains active after stop")
		}
	}
	j = journal{ID: r.Driver.ID(), Phase: "committed", AppliedHash: j.AppliedHash, RejectedHash: j.TargetHash, Error: reason}
	if e := r.persist(j); e != nil {
		return e
	}
	r.setState(func(s *State) {
		s.Phase = "ready"
		s.AppliedHash = j.AppliedHash
		s.RejectedHash = j.RejectedHash
		s.Error = reason
	})
	return nil
}
