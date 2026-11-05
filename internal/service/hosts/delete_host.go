package hosts

import "context"

// DeleteHost deletes an override and returns the id of the node it belonged to.
func (s *Service) DeleteHost(ctx context.Context, id int64) (int64, error) {
	_, _, nodeID, err := s.hostWithInbound(ctx, id)
	if err != nil {
		return 0, err
	}

	return nodeID, s.hosts.DeleteHost(ctx, id)
}
