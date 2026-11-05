package nodes

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// TouchNode updates the status and the time of the last contact with the agent.
func (r *Repository) TouchNode(ctx context.Context, id int64, status panel.NodeStatus, seenAt time.Time) error {
	return sqlite.Affected(r.db.ExecContext(ctx,
		`UPDATE nodes SET status = ?, last_seen = ? WHERE id = ?`,
		status, sqlite.Time(seenAt), id))
}
