package network

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// Installations lists a node's installations.
func (r *Repository) Installations(ctx context.Context, nodeID int64) ([]panel.Installation, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id,node_id,identity,ip,state,last_seen,applied_hash,apply_error
		FROM node_installations WHERE node_id=? ORDER BY id`,
		nodeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []panel.Installation{}

	for rows.Next() {
		var i panel.Installation
		if err = rows.Scan(
			&i.ID,
			&i.NodeID,
			&i.Identity,
			&i.IP,
			&i.State,
			sqlite.NullTime(&i.LastSeen),
			&i.AppliedHash,
			&i.ApplyError,
		); err != nil {
			return nil, err
		}

		out = append(out, i)
	}

	return out, rows.Err()
}
