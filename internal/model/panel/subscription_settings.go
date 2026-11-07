package panel

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits and defaults of the subscription settings.
const (
	DefaultSubscriptionTitle               = "giocondo-vpn"
	DefaultSubscriptionUpdateIntervalHours = 12

	maxSubscriptionTitleRunes          = 128
	maxSubscriptionAnnounceRunes       = 200 // what Happ displays
	maxSubscriptionURLBytes            = 2048
	minSubscriptionUpdateIntervalHours = 1
	maxSubscriptionUpdateIntervalHours = 168
)

// SubscriptionSettings configure what subscriptions tell clients. A new setting needs a field with a default
// here, a check in Validate, a form field on the settings page and its use in the subscription.
type SubscriptionSettings struct {
	Title               string `json:"title"`                 // profile name in the client
	UpdateIntervalHours int    `json:"update_interval_hours"` // how often a client refreshes the subscription

	// Optional headers some clients show; an empty value is not sent.
	SupportURL     string `json:"support_url"`
	ProfilePageURL string `json:"profile_page_url"`
	Announce       string `json:"announce"`

	// ClashRoutingEnabled applies ClashRoutingYAML to Mihomo profiles; the YAML stays saved while it is off.
	ClashRoutingEnabled bool   `json:"clash_routing_enabled"`
	ClashRoutingYAML    string `json:"clash_routing_yaml"`
}

// DefaultSubscriptionSettings returns the settings of a panel that has none stored.
func DefaultSubscriptionSettings() SubscriptionSettings {
	return SubscriptionSettings{
		Title:               DefaultSubscriptionTitle,
		UpdateIntervalHours: DefaultSubscriptionUpdateIntervalHours,
	}
}

// Validate reports the first invalid setting as ErrInvalid.
func (s SubscriptionSettings) Validate() error {
	if strings.TrimSpace(s.Title) == "" {
		return fmt.Errorf("%w: title is required", ErrInvalid)
	}

	if utf8.RuneCountInString(s.Title) > maxSubscriptionTitleRunes {
		return fmt.Errorf("%w: title must be at most %d characters", ErrInvalid, maxSubscriptionTitleRunes)
	}

	if strings.ContainsFunc(s.Title, unicode.IsControl) {
		return fmt.Errorf("%w: title must not contain control characters", ErrInvalid)
	}

	if h := s.UpdateIntervalHours; h < minSubscriptionUpdateIntervalHours || h > maxSubscriptionUpdateIntervalHours {
		return InvalidSubscriptionUpdateInterval()
	}

	if err := s.validateHeaders(); err != nil {
		return err
	}

	if strings.TrimSpace(s.ClashRoutingYAML) == "" {
		if s.ClashRoutingEnabled {
			return fmt.Errorf("%w: Mihomo routing is enabled without rules", ErrInvalid)
		}

		return nil
	}

	if _, err := ParseClashRouting(s.ClashRoutingYAML); err != nil {
		return err
	}

	return nil
}

func (s SubscriptionSettings) validateHeaders() error {
	if !validSubscriptionURL(s.SupportURL) {
		return fmt.Errorf("%w: support link must be an https URL", ErrInvalid)
	}

	if !validSubscriptionURL(s.ProfilePageURL) {
		return fmt.Errorf("%w: profile page must be an https URL", ErrInvalid)
	}

	if utf8.RuneCountInString(s.Announce) > maxSubscriptionAnnounceRunes {
		return fmt.Errorf("%w: announcement must be at most %d characters", ErrInvalid, maxSubscriptionAnnounceRunes)
	}

	if strings.ContainsFunc(s.Announce, unicode.IsControl) {
		return fmt.Errorf("%w: announcement must not contain control characters", ErrInvalid)
	}

	return nil
}

// validSubscriptionURL accepts an empty value, which means the header is not sent. The value goes to a response
// header as is, and clients open it on a tap.
func validSubscriptionURL(raw string) bool {
	if raw == "" {
		return true
	}

	if len(raw) > maxSubscriptionURLBytes || strings.ContainsFunc(raw, unicode.IsControl) {
		return false
	}

	u, err := url.Parse(raw)

	return err == nil && u.Scheme == "https" && u.Host != ""
}

// InvalidSubscriptionUpdateInterval is the error for an update interval that is out of range or not a number.
func InvalidSubscriptionUpdateInterval() error {
	return fmt.Errorf("%w: update interval must be a number of hours from %d to %d",
		ErrInvalid, minSubscriptionUpdateIntervalHours, maxSubscriptionUpdateIntervalHours)
}
