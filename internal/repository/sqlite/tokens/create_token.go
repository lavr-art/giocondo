package tokens

import (
	"context"
	"fmt"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// CreateToken stores an API token (Hash is the token sha256). CreatedAt is the current time.
func (r *Repository) CreateToken(ctx context.Context, t *panel.APIToken) error {
	if t.Name == "" || t.Scope == "" || t.Hash == "" {
		return fmt.Errorf("token name, scope and hash are required")
	}

	t.CreatedAt = sqlite.Now()

	return sqlite.MapErr(r.db.QueryRowContext(ctx,
		`INSERT INTO api_tokens (name, scope, token_hash, created_at)
		VALUES (?, ?, ?, ?) RETURNING id`,
		t.Name, t.Scope, t.Hash, sqlite.Time(t.CreatedAt)).Scan(&t.ID))
}
