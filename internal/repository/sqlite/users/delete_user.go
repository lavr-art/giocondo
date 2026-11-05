package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// DeleteUser deletes a user together with their traffic.
func (r *Repository) DeleteUser(ctx context.Context, id int64) error {
	return sqlite.Affected(r.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id))
}
