package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// ListNodes lists nodes.
func (s *Reader) ListNodes(ctx context.Context) ([]panel.NodeView, error) {
	nodes, err := s.nodes.ListNodes(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]panel.NodeView, 0, len(nodes))
	for _, n := range nodes {
		v, err := s.nodeView(ctx, n)
		if err != nil {
			return nil, err
		}

		out = append(out, v)
	}

	return out, nil
}
