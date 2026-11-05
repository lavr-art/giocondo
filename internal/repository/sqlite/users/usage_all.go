package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// UsageAll is UserUsage for all users in one query.
func (r *Repository) UsageAll(ctx context.Context) (map[int64]panel.Usage, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT users.id,
		COALESCE(SUM(traffic.up), 0) - users.usage_offset_up,
		COALESCE(SUM(traffic.down), 0) - users.usage_offset_down
		FROM users LEFT JOIN traffic ON traffic.user_id = users.id
		GROUP BY users.id, users.usage_offset_up, users.usage_offset_down`)
	if err != nil {
		return nil, sqlite.MapErr(err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64]panel.Usage{}

	for rows.Next() {
		var (
			id int64
			u  panel.Usage
		)
		if err := rows.Scan(&id, &u.Up, &u.Down); err != nil {
			return nil, err
		}

		out[id] = u.Clamp()
	}

	return out, rows.Err()
}
