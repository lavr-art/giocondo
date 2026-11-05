package nodestate

import (
	"context"

	"github.com/artemlavr/giocondo/pkg/logger"
)

// RecordApplied records which config runs on the agent and how the last apply ended.
func (s *Service) RecordApplied(ctx context.Context, a *Agent, hash, applyErr string) {
	inst := s.currentInstallation(ctx, a)

	// Detached from cancellation: it is also called while the stream is closing.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stateWriteTimeout)
	defer cancel()

	if inst == nil {
		s.setNodeApplied(ctx, a.Node.ID, hash, applyErr)

		return
	}

	if err := s.network.ApplyInstallation(ctx, inst.ID, hash, applyErr); err != nil {
		logger.FromCtx(ctx).Error("apply installation", "err", err)
	}
}

func (s *Service) setNodeApplied(ctx context.Context, nodeID int64, hash, applyErr string) {
	key := hash + "\x00" + applyErr

	s.mu.Lock()
	same := s.applied[nodeID] == key
	s.mu.Unlock()

	if same {
		return
	}

	if err := s.nodes.SetNodeApplied(ctx, nodeID, hash, applyErr); err != nil {
		logger.FromCtx(ctx).Error("save applied config hash", "node_id", nodeID, "err", err)

		return
	}

	s.mu.Lock()
	if s.applied == nil {
		s.applied = map[int64]string{}
	}

	s.applied[nodeID] = key
	s.mu.Unlock()
}
