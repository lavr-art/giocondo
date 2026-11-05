package tokens

import (
	"context"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// DeleteToken revokes a token.
func (r *Repository) DeleteToken(ctx context.Context, id int64) error {
	return sqlite.Affected(r.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ?`, id))
}
