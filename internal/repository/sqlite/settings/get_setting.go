package settings

import (
	"context"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// GetSetting returns a stored value, or ErrNotFound if the key has none.
func (r *Repository) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	if err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value); err != nil {
		return "", sqlite.MapErr(err)
	}

	return value, nil
}
