package subscription

// Split routing for clients that receive a ready config (sing-box JSON and
// Mihomo YAML). The data is runetfreedom/russia-v2ray-rules-dat, updated every 6 hours.
// Rule order matters:
//  1. private networks go direct;
//  2. what is blocked in Russia (ru-blocked) goes through the VPN, even if the domain is in the .ru zone;
//  3. Russian resources (ru/su/xn--p1ai zones, category-ru, ru-available-only-inside, geoip ru) go direct;
//  4. everything else goes through the VPN.
// Hiddify and link-based clients configure routing themselves.

const runetfreedomBase = "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release"

// ruleSet is a sing-box rule-set (srs) from the runetfreedom release branch.
type ruleSet struct {
	Tag  string // geosite-ru-blocked
	Path string // path in the release branch
}

func (r ruleSet) URL() string { return runetfreedomBase + "/" + r.Path }

var (
	rsSiteBlocked = ruleSet{"geosite-ru-blocked", "sing-box/rule-set-geosite/geosite-ru-blocked.srs"}
	rsIPBlocked   = ruleSet{"geoip-ru-blocked", "sing-box/rule-set-geoip/geoip-ru-blocked.srs"}
	rsSiteRU      = ruleSet{"geosite-category-ru", "sing-box/rule-set-geosite/geosite-category-ru.srs"}
	rsSiteInside  = ruleSet{"geosite-ru-available-only-inside", "sing-box/rule-set-geosite/geosite-ru-available-only-inside.srs"}
	rsIPRU        = ruleSet{"geoip-ru", "sing-box/rule-set-geoip/geoip-ru.srs"}

	allRuleSets = []ruleSet{rsSiteBlocked, rsIPBlocked, rsSiteRU, rsSiteInside, rsIPRU}
)

// directSuffixes are zones that bypass the VPN (RU, SU, RF) unless the domain is in ru-blocked.
var directSuffixes = []string{"ru", "su", "xn--p1ai"}

// Mihomo geodata: the same categories in geoip.dat / geosite.dat. geosite.dat weighs ~70 MB,
// so the loader is memory-conservative and updates every 3 days.
const (
	geoipDatURL   = runetfreedomBase + "/geoip.dat"
	geositeDatURL = runetfreedomBase + "/geosite.dat"
)

func tags(sets ...ruleSet) []string {
	out := make([]string, len(sets))
	for i, s := range sets {
		out[i] = s.Tag
	}
	return out
}
