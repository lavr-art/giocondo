package nodeconfig

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// EnsureMonitor installs the node monitor.
func (s *Coordinator) EnsureMonitor(ctx context.Context, nodeID int64) (*protocol.User, error) {
	u, created, err := s.allocator.EnsureMonitor(ctx, s.nodes, s.users, nodeID)
	if created {
		s.hooks.Kick()
	}

	return u, err
}
