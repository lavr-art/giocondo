package network

import (
	"context"
	"encoding/json"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// ListNetworkOperations lists queued network operations.
func (r *Repository) ListNetworkOperations(ctx context.Context, nodeID int64) ([]panel.NetworkOperation, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT payload FROM network_operations WHERE (?=0 OR node_id=?) ORDER BY started_at DESC,id DESC`,
		nodeID, nodeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []panel.NetworkOperation{}

	for rows.Next() {
		var (
			data string
			o    panel.NetworkOperation
		)

		if err = rows.Scan(&data); err != nil {
			return nil, err
		}

		if err = json.Unmarshal([]byte(data), &o); err != nil {
			return nil, err
		}

		out = append(out, o)
	}

	return out, rows.Err()
}
