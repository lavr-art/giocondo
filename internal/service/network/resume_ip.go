package network

import (
	"context"
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// ResumeIP creates a new journal entry rather than erasing the failed attempt.
func (s *Service) ResumeIP(
	ctx context.Context,
	id, action string,
	in panel.ResumeIPInput,
) (panel.NetworkOperation, error) {
	s.nodeLock.Lock()

	defer s.nodeLock.Unlock()

	if action != "retry" && action != "rollback" {
		return panel.NetworkOperation{}, operation.Invalid("action must be retry or rollback")
	}

	old, err := s.network.GetNetworkOperation(ctx, id)
	if err != nil {
		return panel.NetworkOperation{}, err
	}

	if old.Status == panel.OperationRunning {
		return panel.NetworkOperation{}, panel.ErrConflict
	}

	if err = s.Unlocked(ctx, old.NodeID); err != nil {
		return panel.NetworkOperation{}, err
	}

	n, err := s.nodes.GetNode(ctx, old.NodeID)
	if err != nil {
		return panel.NetworkOperation{}, err
	}

	installationChanged := n.ActiveInstallation != old.OldInstallation && n.ActiveInstallation != old.NewInstallation
	if installationChanged || (n.IP != old.OldIP && n.IP != old.NewIP) {
		return panel.NetworkOperation{}, operation.Invalid(
			"operation was superseded by another installation; use the current operation",
		)
	}

	if s.dns == nil || !s.probeEnabled {
		return panel.NetworkOperation{}, operation.Invalid("DNS and health checker required")
	}

	op := *old
	op.ID = operationID()
	op.Kind = action
	op.Stage = "reconciling"
	op.Error = ""
	op.Log = ""

	op.FinishedAt = nil
	if err = s.network.CreateNetworkOperation(ctx, &op); err != nil {
		return panel.NetworkOperation{}, err
	}

	if s.executor == nil || !s.executor.Submit(20*time.Minute, func(work context.Context) {
		o := op
		work = logger.With(work, "operation", o.ID, "node_id", o.NodeID)
		s.finishOperation(work, &o, s.runResume(work, &o, *n, action, in))
	}) {
		op.Status = panel.OperationFailed
		op.Error = "application is stopping"
		end := s.hooks.Now()
		op.FinishedAt = &end

		persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()

		if err := s.network.SaveNetworkOperation(persist, &op); err != nil {
			return panel.NetworkOperation{}, err
		}

		return panel.NetworkOperation{}, context.Canceled
	}

	return op, nil
}

// runResume checks that DNS still belongs to the operation and continues it in the requested direction.
func (s *Service) runResume(
	ctx context.Context,
	o *panel.NetworkOperation,
	n panel.Node,
	action string,
	in panel.ResumeIPInput,
) error {
	records, err := s.dns.Lookup(ctx, n.Host)
	if err != nil {
		return err
	}

	if len(records) != 1 {
		return errors.New("DNS conflict")
	}

	if action == "rollback" {
		return s.rollbackOperation(ctx, o, n, records[0], in)
	}

	return s.retryOperation(ctx, o, n, records[0], in)
}

// rollbackOperation returns the node, DNS and active installation to the source address.
func (s *Service) rollbackOperation(
	ctx context.Context,
	o *panel.NetworkOperation,
	n panel.Node,
	record panel.DNSRecord,
	in panel.ResumeIPInput,
) error {
	if record.Content != o.NewIP && record.Content != o.OldIP {
		return errors.New("DNS changed outside this operation")
	}

	// Bring a retired source back only using explicitly supplied credentials.
	if in.SourceSSH != nil {
		if err := s.installer.StartServices(ctx, sourceSSH(in.SourceSSH, o.OldIP)); err != nil {
			return err
		}
	}

	if err := s.network.SetInstallationState(ctx, o.OldInstallation, panel.InstallationActive); err != nil {
		return err
	}

	s.hooks.Kick()

	if err := s.addresses.CheckAddress(ctx, n, o.OldIP); err != nil {
		return err
	}

	ensured, err := s.dns.Ensure(ctx, n.Host, o.OldIP, n.DNSRecordID)
	if err != nil {
		return err
	}

	if err = s.network.SetManagedDNS(ctx, n.ID, n.Host, ensured.ID); err != nil {
		return err
	}

	if err = s.verifyDNS(ctx, n.Host, o.OldIP); err != nil {
		return err
	}

	if err = s.activateInstallation(ctx, n.ID, o.OldInstallation, o.OldIP); err != nil {
		return err
	}

	if o.NewInstallation != o.OldInstallation {
		if err = s.network.SetInstallationState(ctx, o.NewInstallation, panel.InstallationRetired); err != nil {
			return err
		}
	}

	return s.saveStage(ctx, o, "rolled_back")
}

// retryOperation finishes a rotation whose DNS already points at the target address.
func (s *Service) retryOperation(
	ctx context.Context,
	o *panel.NetworkOperation,
	n panel.Node,
	record panel.DNSRecord,
	in panel.ResumeIPInput,
) error {
	if record.Content == o.OldIP {
		return errors.New("DNS still points at old IP; start a new rotation with target credentials")
	}

	if record.Content != o.NewIP {
		return errors.New("DNS changed outside this operation")
	}

	if err := s.addresses.CheckAddress(ctx, n, o.NewIP); err != nil {
		return err
	}

	if err := s.activateInstallation(ctx, n.ID, o.NewInstallation, o.NewIP); err != nil {
		return err
	}

	if o.NewInstallation != o.OldInstallation {
		if err := s.drain(ctx); err != nil {
			return err
		}

		if err := s.network.SetInstallationState(ctx, o.OldInstallation, panel.InstallationRetired); err != nil {
			return err
		}

		s.hooks.Kick()

		if err := s.installer.StopServices(ctx, sourceSSH(in.SourceSSH, o.OldIP)); err != nil {
			return errors.New("old installation revoked; SSH stop failed")
		}
	}

	return s.saveStage(ctx, o, "complete")
}
