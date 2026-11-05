package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// ListUsers returns all users in ascending id order.
func (r *Repository) ListUsers(ctx context.Context) ([]panel.User, error) {
	return r.queryUsers(ctx, `SELECT `+userColumns+` FROM users ORDER BY id`)
}
