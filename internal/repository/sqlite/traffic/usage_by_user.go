package traffic

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// UsageByUser returns a user's traffic over all nodes for the days from through to inclusive (UTC).
func (r *Repository) UsageByUser(ctx context.Context, userID int64, from, to time.Time) (panel.Usage, error) {
	var u panel.Usage

	err := r.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(up), 0), COALESCE(SUM(down), 0) FROM traffic
		WHERE user_id = ? AND day >= ? AND day <= ?`,
		userID, sqlite.Day(from), sqlite.Day(to)).Scan(&u.Up, &u.Down)

	return u, sqlite.MapErr(err)
}
