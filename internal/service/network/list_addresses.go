package network

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// ListAddresses lists a node's addresses.
func (s *Service) ListAddresses(ctx context.Context, id int64) ([]panel.Address, error) {
	if _, err := s.nodes.GetNode(ctx, id); err != nil {
		return nil, err
	}

	return s.network.ListAddresses(ctx, id)
}
