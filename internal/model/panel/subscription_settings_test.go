package panel

import (
	"errors"
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
