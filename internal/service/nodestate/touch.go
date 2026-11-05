package nodestate

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// Touch records that the agent is online or offline.
func (s *Service) Touch(ctx context.Context, a *Agent, st panel.NodeStatus) {
	inst := s.currentInstallation(ctx, a)

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), stateWriteTimeout)
	defer cancel()

	if inst == nil {
		if err := s.nodes.TouchNode(ctx, a.Node.ID, st, s.now()); err != nil {
			logger.FromCtx(ctx).Error("touch node", "node_id", a.Node.ID, "err", err)
		}

		return
	}

	if err := s.network.TouchInstallation(ctx, inst.ID, st, s.now()); err != nil {
		logger.FromCtx(ctx).Error("touch installation", "err", err)
	}
}
