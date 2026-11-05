package users

import (
	"context"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// UpdateUser changes the name, limit, term and enabled flag.
func (s *Service) UpdateUser(
	ctx context.Context,
	id int64,
	in panel.UpdateUserInput,
) (panel.UserView, error) {
	u, err := s.users.ModifyUser(ctx, id, func(u *panel.User) error { return applyUpdate(u, in) })
	if err != nil {
		return panel.UserView{}, err
	}

	// A new limit or term allows the corresponding warning to be sent again.
	w := u.Watch
	if in.TrafficLimitBytes != nil {
		w.WarnedTraffic = false
	}

	if in.ExpiresAt != nil || in.ClearExpiry {
		w.WarnedExpiry = false
	}

	if err := s.resetWarnings(ctx, u, w); err != nil {
		return panel.UserView{}, err
	}

	logger.FromCtx(ctx).Info("user updated", "user", u.Name)
	s.hooks.Kick()

	return s.updated(ctx, id)
}

// applyUpdate applies the partial change in to u. An error rejects the whole change: ModifyUser then
// stores nothing, even if u was partly modified.
func applyUpdate(u *panel.User, in panel.UpdateUserInput) error {
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if err := validName(name); err != nil {
			return err
		}

		u.Name = name
	}

	if in.TrafficLimitBytes != nil {
		if *in.TrafficLimitBytes < 0 {
			return operation.Invalid("traffic_limit_bytes must not be negative")
		}

		u.TrafficLimitBytes = *in.TrafficLimitBytes
	}

	if in.ClearExpiry && in.ExpiresAt != nil {
		return operation.Invalid("use either expires_at or clear_expiry")
	}

	if in.ExpiresAt != nil {
		exp := in.ExpiresAt.UTC()
		u.ExpiresAt = &exp
	}

	if in.ClearExpiry {
		u.ExpiresAt = nil
	}

	if in.Enabled != nil {
		u.Enabled = *in.Enabled
	}

	return nil
}
