package hosts

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// UpdateHost replaces the address, port, SNI, path and order of an override.
func (s *Service) UpdateHost(ctx context.Context, id int64, in panel.HostInput) (panel.HostView, error) {
	h, ib, nodeID, err := s.hostWithInbound(ctx, id)
	if err != nil {
		return panel.HostView{}, err
	}

	node, err := s.nodes.GetNode(ctx, nodeID)
	if err != nil {
		return panel.HostView{}, err
	}

	if err := s.checkHost(*node, ib, &in); err != nil {
		return panel.HostView{}, err
	}

	h.Address, h.Port, h.SNI, h.Path, h.Sort = in.Address, in.Port, in.SNI, in.Path, in.Sort
	if err := s.hosts.UpdateHost(ctx, h); err != nil {
		return panel.HostView{}, err
	}

	return hostView(*h, ib), nil
}
