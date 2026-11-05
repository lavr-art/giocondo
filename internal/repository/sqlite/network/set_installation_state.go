package network

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// SetInstallationState updates an installation state.
func (r *Repository) SetInstallationState(ctx context.Context, id int64, state panel.InstallationState) error {
	return sqlite.Affected(r.db.ExecContext(ctx, `UPDATE node_installations SET state=? WHERE id=?`, state, id))
}
