package subscription

import (
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// Format is the subscription body format.
type Format string

// Subscription formats.
const (
	FormatLinks   Format = panel.SubscriptionFormatLinks   // base64 list of links
	FormatSingBox Format = panel.SubscriptionFormatSingBox // JSON sing-box 1.12+
	FormatClash   Format = panel.SubscriptionFormatClash   // YAML Mihomo
)

// uaRules are checked in order and the first match wins. Hiddify comes first:
// its User-Agent contains both "ClashMeta" and "sing-box", but it gets links and
// turns them into a config for its own sing-box version.
var uaRules = []struct {
	substr string
	format Format
}{
	{"hiddify", FormatLinks},
	{"sing-box", FormatSingBox},
	{"singbox", FormatSingBox},
	{"sfa/", FormatSingBox},
	{"sfi/", FormatSingBox},
	{"sfm/", FormatSingBox},
	{"sft/", FormatSingBox},
	{"karing", FormatSingBox},
	{"clash", FormatClash}, // clash, clashmeta, clash-verge, clashforwindows
	{"mihomo", FormatClash},
	{"flclash", FormatClash},
	{"stash", FormatClash},
}

// DetectFormat picks the format by User-Agent; an unknown client gets links.
func DetectFormat(userAgent string) Format {
	ua := strings.ToLower(userAgent)
	for _, r := range uaRules {
		if strings.Contains(ua, r.substr) {
			return r.format
		}
	}

	return FormatLinks
}

// ChooseFormat picks the format of a response: ?format= first, then the rules of the administrator, then the
// built-in ones. It returns false when ?format= asks for a disabled format, because the client asked for exactly
// that format and could not use another. A client detected by User-Agent gets links instead of a disabled format,
// because links are what any client understands.
func ChooseFormat(s panel.SubscriptionSettings, query, userAgent string) (Format, bool) {
	if format, ok := ParseFormat(query); ok {
		if formatDisabled(s, format) {
			return "", false
		}

		return format, true
	}

	format := detectWithRules(s.UserAgentRules, userAgent)
	if formatDisabled(s, format) {
		return FormatLinks, true
	}

	return format, true
}

func detectWithRules(rules []panel.UserAgentRule, userAgent string) Format {
	ua := strings.ToLower(userAgent)
	for _, r := range rules {
		if strings.Contains(ua, strings.ToLower(r.Substring)) {
			return Format(r.Format)
		}
	}

	return DetectFormat(userAgent)
}

func formatDisabled(s panel.SubscriptionSettings, format Format) bool {
	switch format {
	case FormatSingBox:
		return s.SingBoxDisabled
	case FormatClash:
		return s.ClashDisabled
	case FormatLinks:
		return false
	}

	return false
}

// ParseFormat parses ?format=; an empty string and an unknown value give false.
func ParseFormat(s string) (Format, bool) {
	switch strings.ToLower(s) {
	case "links", "base64", "v2ray":
		return FormatLinks, true
	case "singbox", "sing-box", "json":
		return FormatSingBox, true
	case "clash", "mihomo", "yaml":
		return FormatClash, true
	}

	return "", false
}
