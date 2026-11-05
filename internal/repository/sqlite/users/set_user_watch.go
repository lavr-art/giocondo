package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// SetUserWatch stores the watcher state for a user.
func (r *Repository) SetUserWatch(ctx context.Context, id int64, w panel.UserWatch) error {
	return sqlite.Affected(r.db.ExecContext(ctx,
		`UPDATE users SET last_status = ?, warned_traffic = ?, warned_expiry = ? WHERE id = ?`,
		w.LastStatus, w.WarnedTraffic, w.WarnedExpiry, id))
}
