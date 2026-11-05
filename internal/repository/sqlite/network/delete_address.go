package network

import (
	"context"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// DeleteAddress removes an address.
func (r *Repository) DeleteAddress(ctx context.Context, nodeID, id int64) error {
	return sqlite.Affected(r.db.ExecContext(ctx,
		`DELETE FROM node_addresses WHERE node_id=? AND id=? AND ip<>(SELECT ip FROM nodes WHERE id=?)`,
		nodeID, id, nodeID))
}
