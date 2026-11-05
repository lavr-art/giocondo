package network

import (
	"context"
	"database/sql"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// TouchInstallation records agent activity.
func (r *Repository) TouchInstallation(ctx context.Context, id int64, status panel.NodeStatus, at time.Time) error {
	return r.db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE node_installations SET last_seen=? WHERE id=?`, sqlite.Time(at), id); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx,
			`UPDATE nodes SET status=?,last_seen=? WHERE active_installation=? AND status<>'disabled'`,
			status, sqlite.Time(at), id)

		return err
	})
}
