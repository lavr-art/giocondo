package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// GetNode returns a node by id.
func (r *Repository) GetNode(ctx context.Context, id int64) (*panel.Node, error) {
	return scanNode(r.db.QueryRowContext(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE id = ?`, id))
}
