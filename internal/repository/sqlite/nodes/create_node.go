package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// CreateNode registers a node. Empty status and runtime use the defaults.
func (r *Repository) CreateNode(ctx context.Context, n *panel.Node) error {
	return createNode(ctx, r.db, n)
}
