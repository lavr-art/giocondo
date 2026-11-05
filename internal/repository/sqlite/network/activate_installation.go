package network

import (
	"context"
	"database/sql"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// ActivateInstallation marks an installation active.
func (r *Repository) ActivateInstallation(ctx context.Context, nodeID, id int64, ip string) error {
	return r.db.InTx(ctx, func(tx *sql.Tx) error {
		var count int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM node_addresses WHERE node_id=? AND ip=?`,
			nodeID, ip).Scan(&count); err != nil {
			return err
		}

		if count == 0 {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO node_addresses(node_id,installation_id,ip) VALUES(?,?,?)`,
				nodeID, id, ip); err != nil {
				return sqlite.MapErr(err)
			}
		} else {
			if _, err := tx.ExecContext(ctx,
				`UPDATE node_addresses SET installation_id=? WHERE node_id=? AND ip=?`,
				id, nodeID, ip); err != nil {
				return err
			}
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE nodes SET active_installation=?,ip=?,
				down_notified=CASE WHEN ip<>? THEN FALSE ELSE down_notified END WHERE id=?`,
			id, ip, ip, nodeID); err != nil {
			return err
		}

		if id != 0 {
			if err := sqlite.Affected(tx.ExecContext(ctx,
				`UPDATE node_installations SET state=? WHERE id=? AND node_id=?`,
				panel.InstallationActive, id, nodeID)); err != nil {
				return err
			}

			_, err := tx.ExecContext(ctx,
				`UPDATE nodes SET
					applied_hash=CASE WHEN (SELECT applied_hash FROM node_installations WHERE id=?)<>''
						THEN (SELECT applied_hash FROM node_installations WHERE id=?) ELSE applied_hash END,
					apply_error=(SELECT apply_error FROM node_installations WHERE id=?)
				WHERE id=?`,
				id, id, id, nodeID)

			return err
		}

		return nil
	})
}
