package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// NodeByName returns the node with the given name; ErrNotFound if there is none.
func (r *Repository) NodeByName(ctx context.Context, name string) (*panel.Node, error) {
	return scanNode(r.db.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE name = ?`, name))
}
