package network

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// Unlocked reports whether the network is unlocked.
func (s *Service) Unlocked(ctx context.Context, id int64) error {
	locked, err := s.network.NetworkLocked(ctx, id)
	if err != nil {
		return err
	}

	if locked {
		return panel.ErrConflict
	}

	return nil
}
