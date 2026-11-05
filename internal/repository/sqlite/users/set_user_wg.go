package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// SetUserWG stores a newly issued WireGuard peer of a user who has none and touches no other column,
// so a concurrent change of the user is not overwritten. ErrNotFound means the user is gone or already
// has a peer.
func (r *Repository) SetUserWG(ctx context.Context, id int64, wg *protocol.WGPeer) error {
	return sqlite.Affected(r.db.ExecContext(ctx,
		`UPDATE users SET wg_private_key = ?, wg_public_key = ?, wg_preshared_key = ?, wg_address = ?
		WHERE id = ? AND (wg_public_key IS NULL OR wg_address IS NULL)`,
		append(wgArgs(wg), id)...))
}
