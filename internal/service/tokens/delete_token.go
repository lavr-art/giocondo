package tokens

import (
	"context"

	"github.com/artemlavr/giocondo/pkg/logger"
)

// DeleteToken revokes a token.
func (s *Service) DeleteToken(ctx context.Context, id int64) error {
	if err := s.tokens.DeleteToken(ctx, id); err != nil {
		return err
	}

	logger.FromCtx(ctx).Info("api token deleted", "id", id)

	return nil
}
