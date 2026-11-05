package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// GetUser returns a user by id.
func (r *Repository) GetUser(ctx context.Context, id int64) (*panel.User, error) {
	return scanUser(r.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}
