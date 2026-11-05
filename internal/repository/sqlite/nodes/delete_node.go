package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// DeleteNode deletes a node together with its inbound profiles and traffic.
func (r *Repository) DeleteNode(ctx context.Context, id int64) error {
	return sqlite.Affected(r.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id))
}
