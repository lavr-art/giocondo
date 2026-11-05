// Package users manages VPN users.
package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// UserRepository stores users.
type UserRepository interface {
	CreateUser(ctx context.Context, u *panel.User) error
	DeleteUser(ctx context.Context, id int64) error
	GetUser(ctx context.Context, id int64) (*panel.User, error)
	ListUsers(ctx context.Context) ([]panel.User, error)
	ModifyUser(ctx context.Context, id int64, change func(*panel.User) error) (*panel.User, error)
	ResetUsage(ctx context.Context, userID int64) error
	RotateToken(ctx context.Context, id int64) (string, error)
	SetUserWatch(ctx context.Context, id int64, w panel.UserWatch) error
	UsageAll(ctx context.Context) (map[int64]panel.Usage, error)
	UserUsage(ctx context.Context, userID int64) (panel.Usage, error)
}

// wireGuardIssuer issues the missing WireGuard peers of users.
type wireGuardIssuer interface {
	EnsureWG(ctx context.Context) error
}

// Service owns user operations.
type Service struct {
	hooks  operation.Hooks
	users  UserRepository
	subURL string
	wg     wireGuardIssuer
}

// New returns a user service. Subscription links are built on subURL, and wg issues WireGuard peers
// for a new user.
func New(hooks operation.Hooks, users UserRepository, subURL string, wg wireGuardIssuer) *Service {
	return &Service{hooks: hooks, users: users, subURL: subURL, wg: wg}
}
