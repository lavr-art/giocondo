package hosts

import (
	"context"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// DeleteHost deletes an address.
func (r *Repository) DeleteHost(ctx context.Context, id int64) error {
	return sqlite.Affected(r.db.ExecContext(ctx, `DELETE FROM hosts WHERE id = ?`, id))
}
