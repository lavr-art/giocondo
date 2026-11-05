package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// ListInbounds returns the node protocols in ascending id order.
func (r *Repository) ListInbounds(ctx context.Context, nodeID int64) ([]panel.Inbound, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, node_id, type, tag, port, params FROM inbounds WHERE node_id = ? ORDER BY id`,
		nodeID)
	if err != nil {
		return nil, sqlite.MapErr(err)
	}
	defer func() { _ = rows.Close() }()

	var out []panel.Inbound

	for rows.Next() {
		in, err := scanInbound(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, *in)
	}

	return out, rows.Err()
}
