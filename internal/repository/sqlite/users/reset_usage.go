package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// ResetUsage resets a user's used traffic; the history in traffic remains.
func (r *Repository) ResetUsage(ctx context.Context, userID int64) error {
	return sqlite.Affected(r.db.ExecContext(ctx,
		`UPDATE users SET
		usage_offset_up = COALESCE((SELECT SUM(up) FROM traffic WHERE user_id = users.id), 0),
		usage_offset_down = COALESCE((SELECT SUM(down) FROM traffic WHERE user_id = users.id), 0)
		WHERE id = ?`,
		userID))
}
