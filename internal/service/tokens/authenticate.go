package tokens

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// Authenticate checks a token and returns its scope.
func (s *Service) Authenticate(ctx context.Context, token string) (panel.TokenView, error) {
	if !strings.HasPrefix(token, tokenPrefix) {
		return panel.TokenView{}, ErrUnauthorized
	}

	t, err := s.tokens.TokenByHash(ctx, hashToken(token))
	if errors.Is(err, panel.ErrNotFound) {
		return panel.TokenView{}, ErrUnauthorized
	}

	if err != nil {
		return panel.TokenView{}, err
	}

	now := s.hooks.Now()
	s.touchMu.Lock()
	if s.touched == nil {
		s.touched = map[int64]time.Time{}
	}

	stale := now.Sub(s.touched[t.ID]) >= touchEvery
	if stale {
		s.touched[t.ID] = now
	}
	s.touchMu.Unlock()

	if stale {
		if err := s.tokens.TouchToken(ctx, t.ID, now); err != nil {
			logger.FromCtx(ctx).Warn("touch api token", "id", t.ID, "err", err)
		}

		t.LastUsedAt = &now
	}

	return tokenView(*t), nil
}
