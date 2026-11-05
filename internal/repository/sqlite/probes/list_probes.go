package probes

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// ListProbes lists probe results for a node.
func (r *Repository) ListProbes(ctx context.Context, nodeID int64) ([]panel.ProbeResult, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT node_id,ip,tag,point,error,failures,checked_at FROM node_probes WHERE node_id=? ORDER BY ip,point,tag`,
		nodeID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []panel.ProbeResult{}

	for rows.Next() {
		var (
			p  panel.ProbeResult
			at sqlite.Time
		)
		if err = rows.Scan(&p.NodeID, &p.IP, &p.Tag, &p.Point, &p.Error, &p.Failures, &at); err != nil {
			return nil, err
		}

		p.CheckedAt = time.Time(at)
		out = append(out, p)
	}

	return out, rows.Err()
}
