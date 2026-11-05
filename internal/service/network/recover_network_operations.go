package network

import (
	"context"
	"errors"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// RecoverNetworkOperations reconciles interrupted switches; it never replays SSH credentials.
func (s *Service) RecoverNetworkOperations(ctx context.Context) error {
	ops, err := s.network.ListNetworkOperations(ctx, 0)
	if err != nil {
		return err
	}

	for _, o := range ops {
		if o.Status != panel.OperationRunning {
			continue
		}

		n, err := s.nodes.GetNode(ctx, o.NodeID)
		if err != nil {
			return err
		}

		records, err := s.dns.Lookup(ctx, n.Host)
		if err != nil {
			return err
		}

		if len(records) != 1 {
			return errors.New("cannot reconcile DNS; operation remains locked")
		}

		switch records[0].Content {
		case o.NewIP:
			if err = s.activateInstallation(ctx, n.ID, o.NewInstallation, o.NewIP); err != nil {
				return err
			}

			o.Stage = "switched"
			o.Error = "panel restarted after DNS switch; inspect and retry cleanup"
		case o.OldIP:
			o.Error = "interrupted before DNS switch; retry with SSH credentials if needed"
		default:
			return errors.New("unexpected DNS address; operation remains locked")
		}

		o.Status = panel.OperationFailed
		end := s.hooks.Now()

		o.FinishedAt = &end
		if err = s.network.SaveNetworkOperation(ctx, &o); err != nil {
			return err
		}
	}

	return nil
}
