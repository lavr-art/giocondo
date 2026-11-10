// Package dashboard builds the overview on the admin home page.
package dashboard

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// TrafficRepository aggregates the daily traffic counters.
type TrafficRepository interface {
	UsageByDay(ctx context.Context, from, to time.Time) ([]panel.DayUsage, error)
	UsageByUsers(ctx context.Context, from, to time.Time) (map[int64]panel.Usage, error)
	UsageByNodes(ctx context.Context, from, to time.Time) (map[int64]panel.Usage, error)
}

// userLister lists users with their computed status and warnings.
type userLister interface {
	ListUsers(ctx context.Context) ([]panel.UserView, error)
}

// nodeLister lists nodes with their status and check results.
type nodeLister interface {
	ListNodes(ctx context.Context) ([]panel.NodeView, error)
}

// Service owns the dashboard query.
type Service struct {
	hooks   operation.Hooks
	traffic TrafficRepository
	users   userLister
	nodes   nodeLister
}

// New returns a dashboard service.
func New(hooks operation.Hooks, traffic TrafficRepository, users userLister, nodes nodeLister) *Service {
	return &Service{hooks: hooks, traffic: traffic, users: users, nodes: nodes}
}
