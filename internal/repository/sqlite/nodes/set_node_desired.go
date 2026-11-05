package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// SetNodeDesired records the hash of the config the panel built for the node.
func (r *Repository) SetNodeDesired(ctx context.Context, id int64, hash string) error {
	return sqlite.Affected(r.db.ExecContext(ctx, `UPDATE nodes SET desired_hash = ? WHERE id = ?`, hash, id))
}
