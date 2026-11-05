package tokens

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// TouchToken records the time of last use.
func (r *Repository) TouchToken(ctx context.Context, id int64, at time.Time) error {
	return sqlite.Affected(r.db.ExecContext(ctx,
		`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, sqlite.Time(at), id))
}
