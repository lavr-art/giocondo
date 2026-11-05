package network

import "context"

// DeleteAddress removes an address.
func (s *Service) DeleteAddress(ctx context.Context, id, addressID int64) error {
	s.nodeLock.Lock()
	defer s.nodeLock.Unlock()

	if err := s.Unlocked(ctx, id); err != nil {
		return err
	}

	err := s.network.DeleteAddress(ctx, id, addressID)
	if err == nil {
		s.hooks.Kick()
	}

	return err
}
