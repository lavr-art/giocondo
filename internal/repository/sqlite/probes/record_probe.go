package probes

import (
	"context"
	"database/sql"
	"errors"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// RecordProbe stores a probe result.
func (r *Repository) RecordProbe(ctx context.Context, p panel.ProbeResult) (panel.ProbeResult, error) {
	err := r.db.InTx(ctx, func(tx *sql.Tx) error {
		var prev int

		err := tx.QueryRowContext(ctx,
			`SELECT failures FROM node_probes WHERE node_id=? AND ip=? AND tag=? AND point=?`,
			p.NodeID, p.IP, p.Tag, p.Point).Scan(&prev)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		if p.Error != "" {
			p.Failures = min(prev+1, 3)
		} else {
			p.Failures = 0
		}

		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx,
				`INSERT INTO node_probes(node_id,ip,tag,point,error,failures,checked_at) VALUES(?,?,?,?,?,?,?)`,
				p.NodeID, p.IP, p.Tag, p.Point, p.Error, p.Failures, sqlite.Time(p.CheckedAt))
		} else {
			_, err = tx.ExecContext(ctx,
				`UPDATE node_probes SET error=?,failures=?,checked_at=? WHERE node_id=? AND ip=? AND tag=? AND point=?`,
				p.Error, p.Failures, sqlite.Time(p.CheckedAt), p.NodeID, p.IP, p.Tag, p.Point)
		}

		return err
	})

	return p, err
}
