package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// SetNodeApplied records the hash of the config on the node and the last apply error ("" means success).
func (r *Repository) SetNodeApplied(ctx context.Context, id int64, hash, applyErr string) error {
	return sqlite.Affected(r.db.ExecContext(ctx,
		`UPDATE nodes SET applied_hash = ?, apply_error = ? WHERE id = ?`,
		hash, applyErr, id))
}
