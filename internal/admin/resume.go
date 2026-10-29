package admin

import (
	"context"
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/provision"
	"github.com/artemlavr/giocondo/internal/store"
)

type ResumeIPInput struct {
	SourceSSH *SSHInput `json:"source_ssh,omitempty"`
}

// ResumeIP creates a new journal entry rather than erasing the failed attempt.
func (s *Service) ResumeIP(ctx context.Context, id, action string, in ResumeIPInput) (store.NetworkOperation, error) {
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	if action != "retry" && action != "rollback" {
		return store.NetworkOperation{}, invalid("action must be retry or rollback")
	}
	old, err := s.Store.GetNetworkOperation(ctx, id)
	if err != nil {
		return store.NetworkOperation{}, err
	}
	if old.Status == "running" {
		return store.NetworkOperation{}, store.ErrConflict
	}
	if err = s.unlocked(ctx, old.NodeID); err != nil {
		return store.NetworkOperation{}, err
	}
	n, err := s.Store.GetNode(ctx, old.NodeID)
	if err != nil {
		return store.NetworkOperation{}, err
	}
	if (n.ActiveInstallation != old.OldInstallation && n.ActiveInstallation != old.NewInstallation) || (n.IP != old.OldIP && n.IP != old.NewIP) {
		return store.NetworkOperation{}, invalid("operation was superseded by another installation; use the current operation")
	}
	if s.DNS == nil || s.Checker == nil {
		return store.NetworkOperation{}, invalid("DNS and health checker required")
	}
	op := *old
	op.ID = operationID()
	op.Kind = action
	op.Stage = "reconciling"
	op.Error = ""
	op.Log = ""
	op.FinishedAt = nil
	if err = s.Store.CreateNetworkOperation(ctx, &op); err != nil {
		return store.NetworkOperation{}, err
	}
	bg := s.Background
	if bg == nil {
		bg = context.Background()
	}
	go func(o store.NetworkOperation) {
		work, cancel := context.WithTimeout(bg, 20*time.Minute)
		defer cancel()
		err := func() error {
			records, e := s.DNS.Lookup(work, n.Host)
			if e != nil {
				return e
			}
			if len(records) != 1 {
				return errors.New("DNS conflict")
			}
			if action == "rollback" {
				if records[0].Content != o.NewIP && records[0].Content != o.OldIP {
					return errors.New("DNS changed outside this operation")
				}
				// Bring a retired source back only using explicitly supplied credentials.
				if in.SourceSSH != nil {
					key, e := s.DeployKey()
					if e != nil {
						return e
					}
					source := *in.SourceSSH
					if source.Host == "" {
						source.Host = o.OldIP
					}
					if e = provision.StartServices(work, sshTarget(source), key); e != nil {
						return e
					}
				}
				if e = s.Store.SetInstallationState(work, o.OldInstallation, "active"); e != nil {
					return e
				}
				s.kick()
				if e = s.checkAddress(work, *n, o.OldIP); e != nil {
					return e
				}
				record, e := s.DNS.Ensure(work, n.Host, o.OldIP, n.DNSRecordID)
				if e != nil {
					return e
				}
				if e = s.Store.SetManagedDNS(work, n.ID, n.Host, record.ID); e != nil {
					return e
				}
				if e = s.verifyDNS(work, n.Host, o.OldIP); e != nil {
					return e
				}
				if e = s.activateInstallation(work, n.ID, o.OldInstallation, o.OldIP); e != nil {
					return e
				}
				if o.NewInstallation != o.OldInstallation {
					if e = s.Store.SetInstallationState(work, o.NewInstallation, "retired"); e != nil {
						return e
					}
				}
				return s.saveStage(work, &o, "rolled_back")
			}
			if records[0].Content == o.OldIP {
				return errors.New("DNS still points at old IP; start a new rotation with target credentials")
			}
			if records[0].Content != o.NewIP {
				return errors.New("DNS changed outside this operation")
			}
			if e = s.checkAddress(work, *n, o.NewIP); e != nil {
				return e
			}
			if e = s.activateInstallation(work, n.ID, o.NewInstallation, o.NewIP); e != nil {
				return e
			}
			if o.NewInstallation != o.OldInstallation {
				timer := time.NewTimer(120 * time.Second)
				select {
				case <-work.Done():
					timer.Stop()
					return work.Err()
				case <-timer.C:
				}
				if e = s.Store.SetInstallationState(work, o.OldInstallation, "retired"); e != nil {
					return e
				}
				s.kick()
				source := SSHInput{Host: o.OldIP}
				if in.SourceSSH != nil {
					source = *in.SourceSSH
					if source.Host == "" {
						source.Host = o.OldIP
					}
				}
				key, e := s.DeployKey()
				if e != nil {
					return e
				}
				if e = provision.StopServices(work, sshTarget(source), key); e != nil {
					return errors.New("old installation revoked; SSH stop failed")
				}
			}
			return s.saveStage(work, &o, "complete")
		}()
		end := s.now()
		o.FinishedAt = &end
		o.Status = "ok"
		if err != nil {
			o.Status = "failed"
			o.Error = err.Error()
		}
		persist, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		if e := s.Store.SaveNetworkOperation(persist, &o); e != nil {
			s.log().Error("save resumed operation", "err", e)
		}
	}(op)
	return op, nil
}
