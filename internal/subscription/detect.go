package subscription

import "strings"

// Format is the subscription body format.
type Format string

// Subscription formats.
const (
	FormatLinks   Format = "links"   // base64 list of links
	FormatSingBox Format = "singbox" // JSON sing-box 1.12+
	FormatClash   Format = "clash"   // YAML Mihomo
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
