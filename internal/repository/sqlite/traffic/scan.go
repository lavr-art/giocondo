package traffic

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

func checkDeltas(deltas []panel.TrafficDelta) error {
	for _, d := range deltas {
		if d.Up < 0 || d.Down < 0 {
			return fmt.Errorf("negative traffic delta for user %d node %d", d.UserID, d.NodeID)
		}
	}

	return nil
}

func addTraffic(ctx context.Context, tx *sql.Tx, deltas []panel.TrafficDelta) error {
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO traffic (user_id, node_id, day, up, down) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id, node_id, day)
		DO UPDATE SET up = traffic.up + excluded.up, down = traffic.down + excluded.down`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, d := range deltas {
		if d.Up == 0 && d.Down == 0 {
			continue
		}

		if _, err := stmt.ExecContext(ctx, d.UserID, d.NodeID, sqlite.Day(d.At), d.Up, d.Down); err != nil {
			return err
		}
	}

	return nil
}

// usageBy sums traffic per value of column, which is a constant from the caller, never input.
func (r *Repository) usageBy(ctx context.Context, column string, from, to time.Time) (map[int64]panel.Usage, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+column+`, SUM(up), SUM(down) FROM traffic WHERE day >= ? AND day <= ? GROUP BY `+column,
		sqlite.Day(from), sqlite.Day(to))
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
			return nil, sqlite.MapErr(err)
		}

		out[id] = u
	}

	return out, sqlite.MapErr(rows.Err())
}
