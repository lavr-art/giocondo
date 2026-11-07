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
	maxUserAgentRules                  = 32
	maxUserAgentSubstringRunes         = 64
	minSubscriptionUpdateIntervalHours = 1
	maxSubscriptionUpdateIntervalHours = 168
)

// Subscription formats.
const (
	SubscriptionFormatLinks   = "links"
	SubscriptionFormatSingBox = "singbox"
	SubscriptionFormatClash   = "clash"
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

	// SingBoxDisabled and ClashDisabled turn a format off, so the zero value keeps every format available.
	// Links cannot be turned off: they are what an unknown client gets.
	SingBoxDisabled bool `json:"singbox_disabled"`
	ClashDisabled   bool `json:"clash_disabled"`

	// UserAgentRules pick a format for new clients; they are checked before the built-in rules.
	UserAgentRules []UserAgentRule `json:"user_agent_rules"`

	// ClashRoutingEnabled applies ClashRoutingYAML to Mihomo profiles; the YAML stays saved while it is off.
	ClashRoutingEnabled bool   `json:"clash_routing_enabled"`
	ClashRoutingYAML    string `json:"clash_routing_yaml"`
}

// UserAgentRule gives clients whose User-Agent contains Substring, ignoring case, the format.
type UserAgentRule struct {
	Substring string `json:"substring"`
	Format    string `json:"format"`
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

	if err := validateUserAgentRules(s.UserAgentRules); err != nil {
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

// UserAgentRulesText renders the rules for the form field.
func (s SubscriptionSettings) UserAgentRulesText() string {
	var b strings.Builder
	for _, r := range s.UserAgentRules {
		b.WriteString(r.Substring + "=" + r.Format + "\n")
	}

	return b.String()
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

// ParseUserAgentRules parses the form field: one client=format per line. The last "=" separates them, because a
// format never contains one and a User-Agent may.
func ParseUserAgentRules(raw string) ([]UserAgentRule, error) {
	var rules []UserAgentRule

	for line := range strings.Lines(raw) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		i := strings.LastIndex(line, "=")
		if i <= 0 || strings.TrimSpace(line[:i]) == "" {
			return nil, fmt.Errorf("%w: User-Agent rule %q: expected client=format", ErrInvalid, line)
		}

		rules = append(rules, UserAgentRule{
			Substring: strings.TrimSpace(line[:i]),
			Format:    strings.TrimSpace(line[i+1:]),
		})
	}

	if err := validateUserAgentRules(rules); err != nil {
		return nil, err
	}

	return rules, nil
}

func validateUserAgentRules(rules []UserAgentRule) error {
	if len(rules) > maxUserAgentRules {
		return fmt.Errorf("%w: at most %d User-Agent rules are allowed", ErrInvalid, maxUserAgentRules)
	}

	for _, r := range rules {
		if strings.TrimSpace(r.Substring) == "" || strings.ContainsFunc(r.Substring, unicode.IsControl) {
			return fmt.Errorf("%w: User-Agent rule %q: expected client=format", ErrInvalid, r.Substring)
		}

		if utf8.RuneCountInString(r.Substring) > maxUserAgentSubstringRunes {
			return fmt.Errorf("%w: User-Agent rule %q: a client must be at most %d characters",
				ErrInvalid, r.Substring, maxUserAgentSubstringRunes)
		}

		switch r.Format {
		case SubscriptionFormatLinks, SubscriptionFormatSingBox, SubscriptionFormatClash:
		default:
			return fmt.Errorf("%w: User-Agent rule %q: format must be links, singbox or clash", ErrInvalid, r.Substring)
		}
	}

	return nil
}

// InvalidSubscriptionUpdateInterval is the error for an update interval that is out of range or not a number.
func InvalidSubscriptionUpdateInterval() error {
	return fmt.Errorf("%w: update interval must be a number of hours from %d to %d",
		ErrInvalid, minSubscriptionUpdateIntervalHours, maxSubscriptionUpdateIntervalHours)
}
