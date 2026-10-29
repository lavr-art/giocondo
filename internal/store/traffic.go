package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AddTraffic adds deltas to the daily counters in one transaction.
// Negative deltas are rejected: sing-box counters only grow and the agent handles resets.
func (s *SQLite) AddTraffic(ctx context.Context, deltas []TrafficDelta) error {
	for _, d := range deltas {
		if d.Up < 0 || d.Down < 0 {
			return fmt.Errorf("negative traffic delta for user %d node %d", d.UserID, d.NodeID)
		}
	}
	return mapErr(s.inTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO traffic (user_id, node_id, day, up, down) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (user_id, node_id, day) DO UPDATE SET up = traffic.up + excluded.up, down = traffic.down + excluded.down`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for _, d := range deltas {
			if d.Up == 0 && d.Down == 0 {
				continue
			}
			if _, err := stmt.ExecContext(ctx, d.UserID, d.NodeID, day(d.At), d.Up, d.Down); err != nil {
				return err
			}
		}
		return nil
	}))
}

// UsageByUser returns a user's traffic over all nodes for the days from through to inclusive (UTC).
func (s *SQLite) UsageByUser(ctx context.Context, userID int64, from, to time.Time) (Usage, error) {
	var u Usage
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(up), 0), COALESCE(SUM(down), 0) FROM traffic
		WHERE user_id = ? AND day >= ? AND day <= ?`, userID, day(from), day(to)).Scan(&u.Up, &u.Down)
	return u, mapErr(err)
}
