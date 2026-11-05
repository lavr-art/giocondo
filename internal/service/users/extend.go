package users

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// Extend extends the term by days: from the current term, or from now if it has already expired.
// A user without expiry has nothing to extend.
func (s *Service) Extend(ctx context.Context, id int64, days int) (panel.UserView, error) {
	if days <= 0 || days > 3650 {
		return panel.UserView{}, operation.Invalid("days must be between 1 and 3650")
	}

	now := s.hooks.Now()

	u, err := s.users.ModifyUser(ctx, id, func(u *panel.User) error {
		if u.ExpiresAt == nil {
			return operation.Invalid("user %s has no expiry date", u.Name)
		}

		base := *u.ExpiresAt
		if base.Before(now) {
			base = now
		}

		exp := base.AddDate(0, 0, days).Truncate(time.Second)
		u.ExpiresAt = &exp

		return nil
	})
	if err != nil {
		return panel.UserView{}, err
	}

	exp := *u.ExpiresAt

	w := u.Watch

	w.WarnedExpiry = false
	if err := s.resetWarnings(ctx, u, w); err != nil {
		return panel.UserView{}, err
	}

	logger.FromCtx(ctx).Info("user extended", "user", u.Name, "days", days, "expires_at", exp)
	s.hooks.Kick()

	return s.updated(ctx, id)
}
