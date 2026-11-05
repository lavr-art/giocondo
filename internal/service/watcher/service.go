// Package watcher tracks user watch state.
package watcher

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// userManager builds user views and deletes users.
type userManager interface {
	View(u panel.User, usage panel.Usage) panel.UserView
	DeleteUser(ctx context.Context, id int64) error
}

// Service watches user terms and limits, emits status changes and deletes long-expired users.
type Service struct {
	hooks                 operation.Hooks
	users                 WatchRepository
	autoDeleteExpiredDays int
	manager               userManager
}

// New returns a watcher service. A positive autoDeleteExpiredDays deletes users expired for that many days.
func New(hooks operation.Hooks, users WatchRepository, autoDeleteExpiredDays int, manager userManager) *Service {
	return &Service{hooks: hooks, users: users, autoDeleteExpiredDays: autoDeleteExpiredDays, manager: manager}
}

// WatchRepository stores user watches.
type WatchRepository interface {
	ListUsers(ctx context.Context) ([]panel.User, error)
	SetUserWatch(ctx context.Context, id int64, w panel.UserWatch) error
	UsageAll(ctx context.Context) (map[int64]panel.Usage, error)
}
