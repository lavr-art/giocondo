package network

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// AddAddress adds an address to a node.
func (s *Service) AddAddress(ctx context.Context, id int64, raw string) (panel.Address, error) {
	s.nodeLock.Lock()
	defer s.nodeLock.Unlock()

	if err := s.Unlocked(ctx, id); err != nil {
		return panel.Address{}, err
	}

	ip, err := panel.ParseIPv4(raw)
	if err != nil {
		return panel.Address{}, err
	}

	n, err := s.nodes.GetNode(ctx, id)
	if err != nil {
		return panel.Address{}, err
	}

	if n.IP == "" {
		return panel.Address{}, operation.Invalid("set initial node IP first")
	}

	if err = s.ensureInstallation(ctx, n); err != nil {
		return panel.Address{}, err
	}

	a := panel.Address{NodeID: id, InstallationID: n.ActiveInstallation, IP: ip}

	err = s.network.CreateAddress(ctx, &a)
	if err == nil {
		s.hooks.Kick()
	}

	return a, err
}
