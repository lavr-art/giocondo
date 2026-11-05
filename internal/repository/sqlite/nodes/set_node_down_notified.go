package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// SetNodeDownNotified marks that node_down was sent (true) or the node is back online (false).
func (r *Repository) SetNodeDownNotified(ctx context.Context, id int64, v bool) error {
	return sqlite.Affected(r.db.ExecContext(ctx, `UPDATE nodes SET down_notified = ? WHERE id = ?`, v, id))
}
