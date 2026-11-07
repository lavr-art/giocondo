package subscription

import (
	"testing"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

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

func TestChooseFormat(t *testing.T) {
	const (
		flclash = "FlClash/v0.9.3 clash-verge Platform/macos"
		sfm     = "SFM/1.14.4"
		unknown = "v2rayNG/1.10.5"
	)

	clashOff := panel.SubscriptionSettings{ClashDisabled: true}
	singBoxOff := panel.SubscriptionSettings{SingBoxDisabled: true}
	mine := panel.SubscriptionSettings{UserAgentRules: []panel.UserAgentRule{
		{Substring: "MyClient", Format: panel.SubscriptionFormatClash},
		{Substring: "flclash", Format: panel.SubscriptionFormatLinks},
	}}
	mineOff := panel.SubscriptionSettings{
		ClashDisabled:  true,
		UserAgentRules: mine.UserAgentRules,
	}

	tests := []struct {
		name      string
		settings  panel.SubscriptionSettings
		query, ua string
		want      Format
		wantOK    bool
	}{
		{name: "defaults detect by User-Agent", ua: flclash, want: FormatClash, wantOK: true},
		{name: "defaults detect sing-box", ua: sfm, want: FormatSingBox, wantOK: true},
		{name: "defaults give links to an unknown client", ua: unknown, want: FormatLinks, wantOK: true},
		{name: "query wins over User-Agent", query: "singbox", ua: flclash, want: FormatSingBox, wantOK: true},
		{name: "unknown query falls back to User-Agent", query: "nope", ua: flclash, want: FormatClash, wantOK: true},
		{name: "query for a disabled format is refused", settings: clashOff, query: "clash", ua: flclash},
		{name: "query for disabled sing-box is refused", settings: singBoxOff, query: "singbox", ua: sfm},
		{name: "links cannot be disabled", settings: clashOff, query: "links", ua: flclash, want: FormatLinks, wantOK: true},
		{name: "detected disabled format gets links", settings: clashOff, ua: flclash, want: FormatLinks, wantOK: true},
		{name: "detected disabled sing-box gets links", settings: singBoxOff, ua: sfm, want: FormatLinks, wantOK: true},
		{name: "another format stays available", settings: clashOff, ua: sfm, want: FormatSingBox, wantOK: true},
		{name: "own rule detects a new client", settings: mine, ua: "myclient/1.0", want: FormatClash, wantOK: true},
		{name: "own rule goes before the built-in ones", settings: mine, ua: flclash, want: FormatLinks, wantOK: true},
		{name: "query wins over an own rule", settings: mine, query: "singbox", ua: "MyClient/1.0", want: FormatSingBox, wantOK: true},
		{name: "own rule to a disabled format gets links", settings: mineOff, ua: "MyClient/1.0", want: FormatLinks, wantOK: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ChooseFormat(tt.settings, tt.query, tt.ua)
			if ok != tt.wantOK || got != tt.want {
				t.Fatalf("ChooseFormat() = %q, %v, want %q, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
