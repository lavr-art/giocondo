package network

import (
	"context"
)

// NetworkLocked reports whether the network is locked.
func (r *Repository) NetworkLocked(ctx context.Context, nodeID int64) (bool, error) {
	var count int

	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM network_locks WHERE node_id=?`, nodeID).Scan(&count)

	return count > 0, err
}
