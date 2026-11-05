package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// UserUsage is a user's traffic since the last ResetUsage.
func (r *Repository) UserUsage(ctx context.Context, userID int64) (panel.Usage, error) {
	var u panel.Usage

	err := r.db.QueryRowContext(ctx,
		`SELECT
		COALESCE((SELECT SUM(up) FROM traffic WHERE user_id = users.id), 0) - usage_offset_up,
		COALESCE((SELECT SUM(down) FROM traffic WHERE user_id = users.id), 0) - usage_offset_down
		FROM users WHERE id = ?`,
		userID).Scan(&u.Up, &u.Down)

	return u.Clamp(), sqlite.MapErr(err)
}
