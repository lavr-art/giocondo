package panel

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestSubscriptionSettingsValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*SubscriptionSettings)
		wantErr string // empty means valid
	}{
		{name: "defaults", mutate: func(*SubscriptionSettings) {}},
		{name: "non-ASCII title", mutate: func(s *SubscriptionSettings) { s.Title = "Мой VPN" }},
		{name: "longest title", mutate: func(s *SubscriptionSettings) { s.Title = strings.Repeat("я", 128) }},
		{name: "shortest interval", mutate: func(s *SubscriptionSettings) { s.UpdateIntervalHours = 1 }},
		{name: "longest interval", mutate: func(s *SubscriptionSettings) { s.UpdateIntervalHours = 168 }},
		{name: "empty title", mutate: func(s *SubscriptionSettings) { s.Title = "" }, wantErr: "title is required"},
		{name: "blank title", mutate: func(s *SubscriptionSettings) { s.Title = "  " }, wantErr: "title is required"},
		{
			name:    "title with a line break",
			mutate:  func(s *SubscriptionSettings) { s.Title = "a\r\nb" },
			wantErr: "control characters",
		},
		{
			name:    "title over the limit",
			mutate:  func(s *SubscriptionSettings) { s.Title = strings.Repeat("я", 129) },
			wantErr: "at most 128",
		},
		{name: "zero interval", mutate: func(s *SubscriptionSettings) { s.UpdateIntervalHours = 0 }, wantErr: "from 1 to 168"},
		{
			name:    "interval over the limit",
			mutate:  func(s *SubscriptionSettings) { s.UpdateIntervalHours = 169 },
			wantErr: "from 1 to 168",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := DefaultSubscriptionSettings()
			tt.mutate(&s)

			err := s.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}

				return
			}

			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want ErrInvalid with %q", err, tt.wantErr)
			}
		})
	}
}

func TestSubscriptionSettingsValidateHeaders(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*SubscriptionSettings)
		wantErr string // empty means valid
	}{
		{name: "no headers", mutate: func(*SubscriptionSettings) {}},
		{name: "support link", mutate: func(s *SubscriptionSettings) { s.SupportURL = "https://t.me/support" }},
		{name: "profile page", mutate: func(s *SubscriptionSettings) { s.ProfilePageURL = "https://example.com/vpn" }},
		{name: "longest announcement", mutate: func(s *SubscriptionSettings) { s.Announce = strings.Repeat("я", 200) }},
		{
			name:    "support link over plain HTTP",
			mutate:  func(s *SubscriptionSettings) { s.SupportURL = "http://example.com" },
			wantErr: "support link must be an https URL",
		},
		{
			name:    "profile page without a host",
			mutate:  func(s *SubscriptionSettings) { s.ProfilePageURL = "https://" },
			wantErr: "profile page must be an https URL",
		},
		{
			name:    "support link with another scheme",
			mutate:  func(s *SubscriptionSettings) { s.SupportURL = "javascript:alert(1)" },
			wantErr: "support link must be an https URL",
		},
		{
			name:    "header injection through a link",
			mutate:  func(s *SubscriptionSettings) { s.SupportURL = "https://example.com/\r\nSet-Cookie: a=b" },
			wantErr: "support link must be an https URL",
		},
		{
			name:    "link over the limit",
			mutate:  func(s *SubscriptionSettings) { s.ProfilePageURL = "https://example.com/" + strings.Repeat("a", 2048) },
			wantErr: "profile page must be an https URL",
		},
		{
			name:    "announcement over the limit",
			mutate:  func(s *SubscriptionSettings) { s.Announce = strings.Repeat("я", 201) },
			wantErr: "at most 200",
		},
		{
			name:    "announcement with a line break",
			mutate:  func(s *SubscriptionSettings) { s.Announce = "a\nb" },
			wantErr: "control characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := DefaultSubscriptionSettings()
			tt.mutate(&s)

			err := s.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v", err)
				}

				return
			}

			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want ErrInvalid with %q", err, tt.wantErr)
			}
		})
	}
}

func TestParseUserAgentRules(t *testing.T) {
	got, err := ParseUserAgentRules("  my client/1 = clash \r\n\nkaring=singbox\na=b=links\n")
	if err != nil {
		t.Fatal(err)
	}

	want := []UserAgentRule{
		{Substring: "my client/1", Format: SubscriptionFormatClash},
		{Substring: "karing", Format: SubscriptionFormatSingBox},
		{Substring: "a=b", Format: SubscriptionFormatLinks},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("rules = %+v, want %+v", got, want)
	}

	s := SubscriptionSettings{UserAgentRules: want}
	if text := s.UserAgentRulesText(); text != "my client/1=clash\nkaring=singbox\na=b=links\n" {
		t.Fatalf("text = %q", text)
	}

	if rules, err := ParseUserAgentRules(" \n "); err != nil || rules != nil {
		t.Fatalf("empty text: %+v, %v", rules, err)
	}

	for _, tt := range []struct{ name, raw, want string }{
		{"no format", "karing", "client=format"},
		{"empty substring", "=clash", "client=format"},
		{"unknown format", "karing=json", "links, singbox or clash"},
		{"substring over the limit", strings.Repeat("a", 65) + "=clash", "at most 64"},
		{"too many rules", strings.Repeat("a=clash\n", 33), "at most 32"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseUserAgentRules(tt.raw); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParseUserAgentRules() = %v, want ErrInvalid with %q", err, tt.want)
			}
		})
	}
}

// A rule can reach the settings without the form, so the document is checked as well.
func TestSubscriptionSettingsValidateUserAgentRules(t *testing.T) {
	s := DefaultSubscriptionSettings()

	s.UserAgentRules = []UserAgentRule{{Substring: "a", Format: "json"}}
	if err := s.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown format: %v, want ErrInvalid", err)
	}

	s.UserAgentRules = []UserAgentRule{{Substring: "a\nb", Format: SubscriptionFormatLinks}}
	if err := s.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("control character: %v, want ErrInvalid", err)
	}
}
