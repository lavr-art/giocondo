package nodestate

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// ReconcileOnce rebuilds desired configurations of enabled nodes, including offline nodes.
func (s *Service) ReconcileOnce(ctx context.Context) {
	nodes, err := s.nodes.ListNodes(ctx)
	if err != nil {
		if ctx.Err() == nil {
			logger.FromCtx(ctx).Error("reconcile: list nodes", "err", err)
		}

		return
	}

	for _, n := range nodes {
		if n.Status == panel.NodeDisabled {
			continue
		}

		// The cache is checked against the database: a deleted node's id may be reused by a new one.
		s.mu.Lock()
		if s.desired == nil {
			s.desired = map[int64]string{}
		}

		s.desired[n.ID] = n.DesiredHash
		s.mu.Unlock()

		if _, _, err := s.DesiredConfig(ctx, n.ID); err != nil && ctx.Err() == nil {
			logger.FromCtx(ctx).Error("reconcile: build config", "node", n.Name, "err", err)
		}
	}
}
