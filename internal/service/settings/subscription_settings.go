package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// SubscriptionSettings returns the stored settings over the defaults. A stored document that cannot be used is an
// error rather than the defaults: otherwise clients would silently get settings the administrator did not choose.
func (s *Service) SubscriptionSettings(ctx context.Context) (panel.SubscriptionSettings, error) {
	settings := s.defaults

	doc, err := s.settings.GetSetting(ctx, subscriptionKey)
	if errors.Is(err, panel.ErrNotFound) {
		return settings, nil
	}

	if err != nil {
		return panel.SubscriptionSettings{}, fmt.Errorf("read subscription settings: %w", err)
	}

	if err := json.Unmarshal([]byte(doc), &settings); err != nil {
		return panel.SubscriptionSettings{}, fmt.Errorf("decode subscription settings: %w", err)
	}

	if err := settings.Validate(); err != nil {
		return panel.SubscriptionSettings{}, fmt.Errorf("stored subscription settings: %w", err)
	}

	return settings, nil
}
