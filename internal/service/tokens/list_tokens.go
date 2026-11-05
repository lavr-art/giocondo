package tokens

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// ListTokens returns tokens without secrets.
func (s *Service) ListTokens(ctx context.Context) ([]panel.TokenView, error) {
	list, err := s.tokens.ListTokens(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]panel.TokenView, len(list))
	for i, t := range list {
		out[i] = tokenView(t)
	}

	return out, nil
}
