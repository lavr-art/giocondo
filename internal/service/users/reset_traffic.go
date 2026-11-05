package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// ResetTraffic resets used traffic (e.g. when a package is renewed).
func (s *Service) ResetTraffic(ctx context.Context, id int64) (panel.UserView, error) {
	u, err := s.users.GetUser(ctx, id)
	if err != nil {
		return panel.UserView{}, err
	}

	if err := s.users.ResetUsage(ctx, id); err != nil {
		return panel.UserView{}, err
	}

	w := u.Watch

	w.WarnedTraffic = false
	if err := s.resetWarnings(ctx, u, w); err != nil {
		return panel.UserView{}, err
	}

	s.hooks.Kick()

	return s.updated(ctx, id)
}
