package network

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// CreateNetworkOperation queues a network operation.
func (r *Repository) CreateNetworkOperation(ctx context.Context, o *panel.NetworkOperation) error {
	o.Status = panel.OperationRunning
	o.StartedAt = sqlite.Now()

	data, err := json.Marshal(o)
	if err != nil {
		return err
	}

	return r.db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO network_locks(node_id,operation_id) VALUES(?,?)`,
			o.NodeID, o.ID); err != nil {
			return sqlite.MapErr(err)
		}

		_, err := tx.ExecContext(ctx,
			`INSERT INTO network_operations(id,node_id,status,started_at,payload) VALUES(?,?,?,?,?)`,
			o.ID, o.NodeID, o.Status, sqlite.Time(o.StartedAt), string(data))

		return sqlite.MapErr(err)
	})
}
