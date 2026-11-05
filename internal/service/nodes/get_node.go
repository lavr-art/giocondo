package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// GetNode returns a node by ID.
func (s *Reader) GetNode(ctx context.Context, id int64) (panel.NodeView, error) {
	n, err := s.nodes.GetNode(ctx, id)
	if err != nil {
		return panel.NodeView{}, err
	}

	return s.nodeView(ctx, *n)
}
