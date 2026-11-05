package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// RotateToken gives the user a new subscription token; the old link stops working.
func (r *Repository) RotateToken(ctx context.Context, id int64) (string, error) {
	t, err := sqlite.NewToken()
	if err != nil {
		return "", err
	}

	if err := sqlite.Affected(r.db.ExecContext(ctx, `UPDATE users SET token = ? WHERE id = ?`, t, id)); err != nil {
		return "", err
	}

	return t, nil
}
