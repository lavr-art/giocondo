package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/pkg/logger"
)

// DeleteNode deletes a node, its protocols and traffic. The VPS at the provider is not touched.
func (s *Service) DeleteNode(ctx context.Context, id int64) error {
	s.nodeLock.Lock()
	defer s.nodeLock.Unlock()

	if err := s.network.Unlocked(ctx, id); err != nil {
		return err
	}

	n, err := s.nodes.GetNode(ctx, id)
	if err != nil {
		return err
	}

	if err := s.nodes.DeleteNode(ctx, id); err != nil {
		return err
	}

	logger.FromCtx(ctx).Info("node deleted", "node", n.Name)
	s.hooks.Kick()

	return nil
}
