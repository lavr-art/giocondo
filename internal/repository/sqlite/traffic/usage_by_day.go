package traffic

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// UsageByDay returns the traffic of all users per day for the days from through to inclusive (UTC),
// oldest first; days without traffic are absent.
func (r *Repository) UsageByDay(ctx context.Context, from, to time.Time) ([]panel.DayUsage, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT day, SUM(up), SUM(down) FROM traffic WHERE day >= ? AND day <= ? GROUP BY day ORDER BY day`,
		sqlite.Day(from), sqlite.Day(to))
	if err != nil {
		return nil, sqlite.MapErr(err)
	}
	defer func() { _ = rows.Close() }()

	var out []panel.DayUsage

	for rows.Next() {
		var d panel.DayUsage
		if err := rows.Scan(&d.Day, &d.Up, &d.Down); err != nil {
			return nil, sqlite.MapErr(err)
		}

		d.Day = d.Day.UTC()
		out = append(out, d)
	}

	return out, sqlite.MapErr(rows.Err())
}
