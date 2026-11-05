package nodestate

import (
	"context"
	"errors"

	"github.com/artemlavr/giocondo/internal/service/nodeconfig"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// DesiredConfig builds the node config from the database, records its hash as desired and returns both.
func (s *Service) DesiredConfig(ctx context.Context, nodeID int64) ([]byte, string, error) {
	if s.allocator == nil {
		return nil, "", errors.New("node state has no WireGuard allocator")
	}

	data, hash, err := nodeconfig.Prepare(ctx, s.allocator, s.nodes, s.users, nodeID, s.now(), s.options)
	if err != nil {
		return nil, "", err
	}

	s.setDesired(ctx, nodeID, hash)

	return data, hash, nil
}

// setDesired writes the built config hash to nodes.desired_hash if it changed.
func (s *Service) setDesired(ctx context.Context, nodeID int64, hash string) {
	s.mu.Lock()
	same := s.desired[nodeID] == hash
	s.mu.Unlock()

	if same {
		return
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stateWriteTimeout)
	defer cancel()

	if err := s.nodes.SetNodeDesired(ctx, nodeID, hash); err != nil {
		logger.FromCtx(ctx).Error("save desired config hash", "node_id", nodeID, "err", err)

		return
	}

	s.mu.Lock()
	if s.desired == nil {
		s.desired = map[int64]string{}
	}

	s.desired[nodeID] = hash
	s.mu.Unlock()
}
