package network

import (
	"context"
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// RotateIP rotates a node's IP address.
func (s *Service) RotateIP(
	ctx context.Context,
	id int64,
	in panel.RotateIPInput,
) (panel.NetworkOperation, error) {
	s.nodeLock.Lock()
	defer s.nodeLock.Unlock()

	n, err := s.nodes.GetNode(ctx, id)
	if err != nil {
		return panel.NetworkOperation{}, err
	}

	if in.IP, err = s.validateRotation(n, in); err != nil {
		return panel.NetworkOperation{}, err
	}

	if err = s.Unlocked(ctx, id); err != nil {
		return panel.NetworkOperation{}, err
	}

	if err = s.checkDNSMatchesNode(ctx, n); err != nil {
		return panel.NetworkOperation{}, err
	}

	if err = s.ensureInstallation(ctx, n); err != nil {
		return panel.NetworkOperation{}, err
	}

	if in.Mode == panel.RotateExistingIP {
		if err = s.checkInPool(ctx, n, in.IP); err != nil {
			return panel.NetworkOperation{}, err
		}
	}

	op := panel.NetworkOperation{
		ID:              operationID(),
		NodeID:          id,
		Kind:            in.Mode,
		Stage:           "preparing",
		OldIP:           n.IP,
		NewIP:           in.IP,
		OldInstallation: n.ActiveInstallation,
		NewInstallation: n.ActiveInstallation,
	}
	if err = s.network.CreateNetworkOperation(ctx, &op); err != nil {
		return panel.NetworkOperation{}, err
	}
	// Store only public operation metadata; credentials remain in this bounded goroutine.
	o := op
	if s.executor == nil || !s.executor.Submit(25*time.Minute, func(ctx context.Context) {
		s.runRotation(logger.With(ctx, "operation", o.ID, "node_id", o.NodeID), &o, in)
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

// validateRotation checks the request against the node state and returns the normalized target IP.
func (s *Service) validateRotation(n *panel.Node, in panel.RotateIPInput) (string, error) {
	if !n.ManagedDNS || s.dns == nil {
		return "", operation.Invalid("enable managed DNS first")
	}

	if !s.probeEnabled {
		return "", operation.Invalid("protocol checker is not configured")
	}

	if n.Status == panel.NodeDisabled {
		return "", operation.Invalid("node is disabled")
	}

	ip, err := panel.ParseIPv4(in.IP)
	if err != nil {
		return "", err
	}

	if ip == n.IP {
		return "", operation.Invalid("address is already active")
	}

	if in.Mode != panel.RotateExistingIP && in.Mode != panel.RotateSSH {
		return "", operation.Invalid("mode must be existing_ip or ssh")
	}

	if in.Mode == panel.RotateSSH && (n.Runtime == panel.RuntimeLocal || in.SSH == nil) {
		return "", operation.Invalid("SSH target is required for a remote node")
	}

	return ip, nil
}

// checkDNSMatchesNode refuses to start while DNS still reflects an unfinished operation or an external change.
func (s *Service) checkDNSMatchesNode(ctx context.Context, n *panel.Node) error {
	records, err := s.dns.Lookup(ctx, n.Host)
	if err != nil {
		return err
	}

	if len(records) != 1 || records[0].ID != n.DNSRecordID || records[0].Content != n.IP ||
		records[0].Type != "A" || records[0].Proxied {
		return operation.Invalid("DNS differs from active node state; reconcile the previous operation first")
	}

	return nil
}

func (s *Service) checkInPool(ctx context.Context, n *panel.Node, ip string) error {
	addresses, err := s.network.ListAddresses(ctx, n.ID)
	if err != nil {
		return err
	}

	for _, a := range addresses {
		if a.IP == ip && a.InstallationID == n.ActiveInstallation {
			return nil
		}
	}

	return operation.Invalid("address is not in this installation's pool")
}

func (s *Service) runRotation(ctx context.Context, o *panel.NetworkOperation, in panel.RotateIPInput) {
	s.finishOperation(ctx, o, s.performRotation(ctx, o, in))
}

func (s *Service) performRotation(
	ctx context.Context,
	o *panel.NetworkOperation,
	in panel.RotateIPInput,
) error {
	n, err := s.nodes.GetNode(ctx, o.NodeID)
	if err != nil {
		return err
	}

	if in.Mode == panel.RotateSSH {
		if err = s.installCandidate(ctx, o, n, in); err != nil {
			return err
		}
	}

	if err = s.switchToTarget(ctx, o, n); err != nil {
		return err
	}

	if err = s.addresses.CheckAddress(ctx, *n, o.NewIP); err != nil {
		return err
	}

	if in.Mode == panel.RotateSSH {
		if err = s.retireSource(ctx, o, in); err != nil {
			return err
		}
	}

	return s.saveStage(ctx, o, "complete")
}

// installCandidate installs a new installation on the target server and waits until it runs the desired config.
func (s *Service) installCandidate(
	ctx context.Context,
	o *panel.NetworkOperation,
	n *panel.Node,
	in panel.RotateIPInput,
) error {
	i := &panel.Installation{
		NodeID: n.ID, Identity: "install-" + o.ID, IP: o.NewIP, State: panel.InstallationCandidate,
	}
	if err := s.network.CreateInstallation(ctx, i); err != nil {
		return err
	}

	o.NewInstallation = i.ID
	if err := s.saveStage(ctx, o, "installing"); err != nil {
		return err
	}

	if err := s.installer.InstallCandidate(ctx, n, *i, *in.SSH, in.SourceSSH, in.IP); err != nil {
		return err
	}

	if err := s.saveStage(ctx, o, "waiting_config"); err != nil {
		return err
	}

	return s.waitCandidate(ctx, i.Identity, n.ID)
}

func (s *Service) waitCandidate(ctx context.Context, identity string, nodeID int64) error {
	for {
		candidate, err := s.network.InstallationByIdentity(ctx, identity)
		if err != nil {
			return err
		}

		current, err := s.nodes.GetNode(ctx, nodeID)
		if err != nil {
			return err
		}

		if candidate.ApplyError != "" {
			return errors.New("candidate configuration failed")
		}

		if candidate.LastSeen != nil && candidate.AppliedHash != "" && candidate.AppliedHash == current.DesiredHash {
			return nil
		}

		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()

			return ctx.Err()
		case <-timer.C:
		}
	}
}

// switchToTarget checks the target, moves DNS and the active installation to it and adds it to the pool.
func (s *Service) switchToTarget(ctx context.Context, o *panel.NetworkOperation, n *panel.Node) error {
	if err := s.saveStage(ctx, o, "checking"); err != nil {
		return err
	}

	if err := s.addresses.CheckAddress(ctx, *n, o.NewIP); err != nil {
		return err
	}

	if err := s.saveStage(ctx, o, "switching_dns"); err != nil {
		return err
	}

	record, err := s.dns.Ensure(ctx, n.Host, o.NewIP, n.DNSRecordID)
	if err != nil {
		return err
	}

	if err = s.network.SetManagedDNS(ctx, n.ID, n.Host, record.ID); err != nil {
		return err
	}

	if err = s.verifyDNS(ctx, n.Host, o.NewIP); err != nil {
		return err
	}

	if err = s.activateInstallation(ctx, n.ID, o.NewInstallation, o.NewIP); err != nil {
		return err
	}

	if err = s.ensurePoolAddress(ctx, n.ID, o.NewInstallation, o.NewIP); err != nil {
		return err
	}

	return s.saveStage(ctx, o, "switched")
}

func (s *Service) ensurePoolAddress(ctx context.Context, nodeID, installationID int64, ip string) error {
	addresses, err := s.network.ListAddresses(ctx, nodeID)
	if err != nil {
		return err
	}

	for _, a := range addresses {
		if a.IP == ip {
			return nil
		}
	}

	return s.network.CreateAddress(ctx, &panel.Address{NodeID: nodeID, InstallationID: installationID, IP: ip})
}

// retireSource revokes the old installation after the drain and stops it over SSH; the server is never deleted.
func (s *Service) retireSource(ctx context.Context, o *panel.NetworkOperation, in panel.RotateIPInput) error {
	if err := s.saveStage(ctx, o, "draining"); err != nil {
		return err
	}

	if err := s.drain(ctx); err != nil {
		return err
	}
	// Revoke the old generation even if SSH is unavailable.
	if err := s.network.SetInstallationState(ctx, o.OldInstallation, panel.InstallationRetired); err != nil {
		return err
	}

	s.hooks.Kick()

	if err := s.installer.StopServices(ctx, sourceSSH(in.SourceSSH, o.OldIP)); err != nil {
		return errors.New(
			"IP switched; old installation revoked, SSH stop failed; retry cleanup with source SSH credentials",
		)
	}

	return nil
}
