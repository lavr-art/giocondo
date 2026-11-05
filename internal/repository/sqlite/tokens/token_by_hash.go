package tokens

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// TokenByHash finds a token by sha256.
func (r *Repository) TokenByHash(ctx context.Context, hash string) (*panel.APIToken, error) {
	return scanToken(r.db.QueryRowContext(ctx, `SELECT `+tokenColumns+` FROM api_tokens WHERE token_hash = ?`, hash))
}
