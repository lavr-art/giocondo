package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// DeleteInbound deletes a protocol.
func (r *Repository) DeleteInbound(ctx context.Context, id int64) error {
	return sqlite.Affected(r.db.ExecContext(ctx, `DELETE FROM inbounds WHERE id = ?`, id))
}
