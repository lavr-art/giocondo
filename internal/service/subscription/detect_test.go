package subscription

import "testing"

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		ua   string
		want Format
	}{
		{"HiddifyNext/2.5.7 (android) like ClashMeta v2ray sing-box", FormatLinks},
		{"Hiddify/3.0.0 (ios)", FormatLinks},
		{"Happ/1.9.2", FormatLinks},
		{"v2rayNG/1.10.5", FormatLinks},
		{"v2rayN/7.12.5", FormatLinks},
		{"Streisand/1.6", FormatLinks},
		{"V2Box 2.0 (iOS)", FormatLinks},
		{"Shadowrocket/2.2.58 CFNetwork/1568.100.1 Darwin/24.0.0", FormatLinks},
		{"SFA/1.12.4 (Android 14; sing-box 1.12.4; language en)", FormatSingBox},
		{"SFI/1.11.0 (iOS 17.0; sing-box 1.11.0)", FormatSingBox},
		{"sing-box 1.14.4", FormatSingBox},
		{"Karing/1.1.2", FormatSingBox},
		{"clash-verge/v2.2.3", FormatClash},
		{"ClashMetaForAndroid/2.11.1.Meta", FormatClash},
		{"FlClash/v0.8.80", FormatClash},
		{"mihomo/1.19.0", FormatClash},
		{"Stash/2.4.7 Clash/1.9.0", FormatClash},
		{"curl/8.5.0", FormatLinks},
		{"", FormatLinks},
	}
	for _, tt := range tests {
		if got := DetectFormat(tt.ua); got != tt.want {
			t.Errorf("DetectFormat(%q) = %s, want %s", tt.ua, got, tt.want)
		}
	}
}
