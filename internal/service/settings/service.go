// Package settings manages panel settings changed at runtime.
package settings

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

const subscriptionKey = "subscription"

// Repository stores settings documents by key.
type Repository interface {
	GetSetting(ctx context.Context, key string) (string, error)
	PutSetting(ctx context.Context, key, value string, at time.Time) error
}

// Service reads and changes settings. They change only subscriptions, not node configs, so changing
// them does not wake Sync.
type Service struct {
	hooks    operation.Hooks
	settings Repository
	defaults panel.SubscriptionSettings
}

// New returns a settings service; defaults apply to whatever is not stored.
func New(hooks operation.Hooks, settings Repository, defaults panel.SubscriptionSettings) *Service {
	return &Service{hooks: hooks, settings: settings, defaults: defaults}
}
