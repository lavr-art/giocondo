package users

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// ActiveUsers returns users allowed to connect: enabled, term not expired,
// used traffic (since the last ResetUsage) below the limit (limit 0 means unlimited).
func (r *Repository) ActiveUsers(ctx context.Context, at time.Time) ([]panel.User, error) {
	return r.queryUsers(ctx, `SELECT `+userColumns+` FROM users
		WHERE enabled
		  AND (expires_at IS NULL OR expires_at > ?)
		  AND (traffic_limit_bytes = 0 OR traffic_limit_bytes >
		       (SELECT
                  CASE WHEN COALESCE(SUM(up), 0) > users.usage_offset_up
                       THEN COALESCE(SUM(up), 0) - users.usage_offset_up ELSE 0 END
                + CASE WHEN COALESCE(SUM(down), 0) > users.usage_offset_down
                       THEN COALESCE(SUM(down), 0) - users.usage_offset_down ELSE 0 END
                FROM traffic WHERE traffic.user_id = users.id))
		ORDER BY id`, sqlite.Time(at))
}
