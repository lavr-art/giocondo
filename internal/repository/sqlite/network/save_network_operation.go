package network

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// SaveNetworkOperation stores a network operation.
func (r *Repository) SaveNetworkOperation(ctx context.Context, o *panel.NetworkOperation) error {
	data, err := json.Marshal(o)
	if err != nil {
		return err
	}

	return r.db.InTx(ctx, func(tx *sql.Tx) error {
		if err := sqlite.Affected(tx.ExecContext(ctx,
			`UPDATE network_operations SET status=?,payload=? WHERE id=?`,
			o.Status, string(data), o.ID)); err != nil {
			return err
		}

		if o.Status != panel.OperationRunning {
			_, err = tx.ExecContext(ctx, `DELETE FROM network_locks WHERE node_id=? AND operation_id=?`, o.NodeID, o.ID)
		}

		return err
	})
}
