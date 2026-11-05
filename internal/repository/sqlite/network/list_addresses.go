package network

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// ListAddresses lists a node's addresses.
func (r *Repository) ListAddresses(ctx context.Context, nodeID int64) ([]panel.Address, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id,node_id,installation_id,ip FROM node_addresses WHERE node_id=? ORDER BY id`,
		nodeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []panel.Address{}

	for rows.Next() {
		var a panel.Address
		if err = rows.Scan(&a.ID, &a.NodeID, &a.InstallationID, &a.IP); err != nil {
			return nil, err
		}

		out = append(out, a)
	}

	return out, rows.Err()
}
