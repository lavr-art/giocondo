package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// GetUserByToken returns a user by subscription token.
func (r *Repository) GetUserByToken(ctx context.Context, token string) (*panel.User, error) {
	return scanUser(r.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE token = ?`, token))
}
