package traffic

import (
	"context"
	"database/sql"
	"errors"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// RecordTrafficBatch records one agent statistics batch at most once. The agent resends a batch with
// the same seq until it is acknowledged, so a batch whose seq is not above the last one recorded for
// the node's instance is skipped; the check and the write are one transaction, so a restart of the panel
// or a second stream of the node cannot count it again. A new instance restarts seq. An agent that
// reports no instance cannot be deduplicated: its batches are always recorded.
func (r *Repository) RecordTrafficBatch(
	ctx context.Context,
	nodeID int64,
	instance string,
	seq uint64,
	deltas []panel.TrafficDelta,
) error {
	if err := checkDeltas(deltas); err != nil {
		return err
	}

	return sqlite.MapErr(r.db.InTx(ctx, func(tx *sql.Tx) error {
		if instance == "" {
			return addTraffic(ctx, tx, deltas)
		}

		var (
			lastInstance string
			lastSeq      int64
		)

		err := tx.QueryRowContext(ctx, `SELECT instance_id, seq FROM node_stats_seq WHERE node_id = ?`, nodeID).
			Scan(&lastInstance, &lastSeq)

		switch {
		case err == nil && lastInstance == instance && int64(seq) <= lastSeq:
			return nil
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return err
		}

		if err := addTraffic(ctx, tx, deltas); err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `INSERT INTO node_stats_seq (node_id, instance_id, seq) VALUES (?, ?, ?)
			ON CONFLICT (node_id) DO UPDATE SET instance_id = excluded.instance_id, seq = excluded.seq`,
			nodeID, instance, int64(seq))

		return err
	}))
}
