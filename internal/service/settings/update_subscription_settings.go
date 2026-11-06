package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// UpdateSubscriptionSettings validates and stores the whole document.
func (s *Service) UpdateSubscriptionSettings(ctx context.Context, in panel.SubscriptionSettings) error {
	in.Title = strings.TrimSpace(in.Title)
	if err := in.Validate(); err != nil {
		return err
	}

	doc, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("encode subscription settings: %w", err)
	}

	return s.settings.PutSetting(ctx, subscriptionKey, string(doc), s.hooks.Now())
}
