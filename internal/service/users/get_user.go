package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// GetUser returns a user.
func (s *Service) GetUser(ctx context.Context, id int64) (panel.UserView, error) {
	u, err := s.users.GetUser(ctx, id)
	if err != nil {
		return panel.UserView{}, err
	}

	usage, err := s.users.UserUsage(ctx, id)
	if err != nil {
		return panel.UserView{}, err
	}

	return s.View(*u, usage), nil
}
