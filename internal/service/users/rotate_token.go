package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// RotateToken issues a new subscription link; the old one stops working.
func (s *Service) RotateToken(ctx context.Context, id int64) (panel.UserView, error) {
	if _, err := s.users.RotateToken(ctx, id); err != nil {
		return panel.UserView{}, err
	}

	return s.updated(ctx, id)
}
