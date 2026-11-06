package settings

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// PutSetting stores the value of key, replacing the previous one.
func (r *Repository) PutSetting(ctx context.Context, key, value string, at time.Time) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, sqlite.Time(at))

	return sqlite.MapErr(err)
}
