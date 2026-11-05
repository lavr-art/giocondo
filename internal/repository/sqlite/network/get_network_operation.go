package network

import (
	"context"
	"encoding/json"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// GetNetworkOperation returns a network operation.
func (r *Repository) GetNetworkOperation(ctx context.Context, id string) (*panel.NetworkOperation, error) {
	var data string
	if err := r.db.QueryRowContext(ctx, `SELECT payload FROM network_operations WHERE id=?`, id).Scan(&data); err != nil {
		return nil, sqlite.MapErr(err)
	}

	var o panel.NetworkOperation

	err := json.Unmarshal([]byte(data), &o)

	return &o, err
}
