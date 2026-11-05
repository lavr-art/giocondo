package network

import (
	"context"
	"database/sql"
)

// ApplyInstallation applies a pending installation.
func (r *Repository) ApplyInstallation(ctx context.Context, id int64, hash, applyErr string) error {
	return r.db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE node_installations SET applied_hash=?,apply_error=? WHERE id=?`,
			hash, applyErr, id); err != nil {
			return err
		}

		_, err := tx.ExecContext(ctx,
			`UPDATE nodes SET applied_hash=?,apply_error=? WHERE active_installation=?`,
			hash, applyErr, id)

		return err
	})
}
