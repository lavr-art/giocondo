package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// DeleteUser deletes a user and their traffic.
func (s *Service) DeleteUser(ctx context.Context, id int64) error {
	v, err := s.GetUser(ctx, id)
	if err != nil {
		return err
	}

	if err := s.users.DeleteUser(ctx, id); err != nil {
		return err
	}

	logger.FromCtx(ctx).Info("user deleted", "user", v.Name)
	s.hooks.Kick()
	s.hooks.EmitUser(panel.EventUserDeleted, v)

	return nil
}
