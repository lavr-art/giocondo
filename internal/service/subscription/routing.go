package subscription

// Split routing for clients that receive a ready config (sing-box JSON and Mihomo YAML) or a routing
// profile in a response header (Happ). The data is runetfreedom/russia-v2ray-rules-dat, updated every 6 hours;
// Mihomo clients get it converted by deploy/rules/build.sh and published daily.
// Rule order matters:
//  1. private networks go direct;
//  2. what is blocked in Russia (ru-blocked) goes through the VPN, even if the domain is in the .ru zone;
//  3. Russian resources (ru/su/xn--p1ai zones, category-ru, ru-available-only-inside, geoip ru) go direct;
//  4. everything else goes through the VPN.
// Happ places every domain rule before the IP rules, so a domain in a direct zone goes direct even if its IP is
// in ru-blocked. Hiddify ignores routing from a subscription, and other link-based clients configure it themselves.

const (
	runetfreedomBase = "https://raw.githubusercontent.com/runetfreedom/russia-v2ray-rules-dat/release"
	mihomoRulesBase  = "https://github.com/lavr-art/giocondo/releases/download/rules"

	behaviorDomain = "domain"
	behaviorIPCIDR = "ipcidr"
)

// ruleSet is a sing-box rule-set (srs) from the runetfreedom release branch.
type ruleSet struct {
	Tag      string // geosite-ru-blocked
	Path     string // path in the release branch
	Behavior string // Mihomo rule-provider behavior
}

func (r ruleSet) URL() string { return runetfreedomBase + "/" + r.Path }

// MihomoURL is where deploy/rules/build.sh publishes the set for Mihomo.
func (r ruleSet) MihomoURL() string { return mihomoRulesBase + "/" + r.Tag + ".mrs" }

var (
	rsSiteBlocked = ruleSet{"geosite-ru-blocked", "sing-box/rule-set-geosite/geosite-ru-blocked.srs", behaviorDomain}
	rsIPBlocked   = ruleSet{"geoip-ru-blocked", "sing-box/rule-set-geoip/geoip-ru-blocked.srs", behaviorIPCIDR}
	rsSiteRU      = ruleSet{"geosite-category-ru", "sing-box/rule-set-geosite/geosite-category-ru.srs", behaviorDomain}
	rsSiteInside  = ruleSet{
		"geosite-ru-available-only-inside",
		"sing-box/rule-set-geosite/geosite-ru-available-only-inside.srs",
		behaviorDomain,
	}
	rsIPRU = ruleSet{"geoip-ru", "sing-box/rule-set-geoip/geoip-ru.srs", behaviorIPCIDR}

	allRuleSets = []ruleSet{rsSiteBlocked, rsIPBlocked, rsSiteRU, rsSiteInside, rsIPRU}
)

// directSuffixes are zones that bypass the VPN (RU, SU, RF) unless the domain is in ru-blocked.
var directSuffixes = []string{"ru", "su", "xn--p1ai"}

// Happ geodata: the same categories in geoip.dat / geosite.dat.
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
