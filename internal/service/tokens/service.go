// Package tokens manages API tokens.
package tokens

import (
	"context"
	"sync"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// TokenRepository stores API tokens.
type TokenRepository interface {
	CreateToken(ctx context.Context, t *panel.APIToken) error
	DeleteToken(ctx context.Context, id int64) error
	ListTokens(ctx context.Context) ([]panel.APIToken, error)
	TokenByHash(ctx context.Context, hash string) (*panel.APIToken, error)
	TouchToken(ctx context.Context, id int64, at time.Time) error
}

// Service owns authentication and its last-used write throttle.
type Service struct {
	hooks   operation.Hooks
	tokens  TokenRepository
	touchMu sync.Mutex
	touched map[int64]time.Time
}

// New returns a token service.
func New(hooks operation.Hooks, tokens TokenRepository) *Service {
	return &Service{hooks: hooks, tokens: tokens}
}
