package traffic

import (
	"context"
	"database/sql"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// AddTraffic adds deltas to the daily counters in one transaction.
// Negative deltas are rejected: sing-box counters only grow and the agent handles resets.
func (r *Repository) AddTraffic(ctx context.Context, deltas []panel.TrafficDelta) error {
	if err := checkDeltas(deltas); err != nil {
		return err
	}

	return sqlite.MapErr(r.db.InTx(ctx, func(tx *sql.Tx) error { return addTraffic(ctx, tx, deltas) }))
}
