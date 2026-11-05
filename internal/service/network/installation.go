package network

import (
	"context"
	"errors"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

func (s *Service) ensureInstallation(ctx context.Context, n *panel.Node) error {
	if n.ActiveInstallation != 0 {
		return nil
	}

	i, err := s.network.InstallationByIdentity(ctx, n.Name)
	if errors.Is(err, panel.ErrNotFound) {
		i = &panel.Installation{NodeID: n.ID, Identity: n.Name, IP: n.IP, State: panel.InstallationActive}
		err = s.network.CreateInstallation(ctx, i)
	}

	if err != nil {
		return err
	}

	if err = s.network.ActivateInstallation(ctx, n.ID, i.ID, n.IP); err != nil {
		return err
	}

	n.ActiveInstallation = i.ID

	addresses, err := s.network.ListAddresses(ctx, n.ID)
	if err != nil {
		return err
	}

	for _, a := range addresses {
		if a.IP == n.IP {
			return nil
		}
	}

	return s.network.CreateAddress(ctx, &panel.Address{NodeID: n.ID, InstallationID: i.ID, IP: n.IP})
}

func (s *Service) activateInstallation(ctx context.Context, nodeID, id int64, ip string) error {
	s.nodeLock.Lock()
	defer s.nodeLock.Unlock()

	return s.network.ActivateInstallation(ctx, nodeID, id, ip)
}
