package traffic

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// UsageByNodes returns each node's traffic over all users for the days from through to inclusive (UTC).
func (r *Repository) UsageByNodes(ctx context.Context, from, to time.Time) (map[int64]panel.Usage, error) {
	return r.usageBy(ctx, "node_id", from, to)
}
