package panel

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

// Names of the proxy groups every Mihomo profile has.
const (
	ClashGroupProxy = "proxy"
	ClashGroupAuto  = "auto"
)

// ClashBuiltinRuleSets are the rule providers of the built-in routing; custom rules may use them.
var ClashBuiltinRuleSets = []string{
	"geosite-ru-blocked",
	"geoip-ru-blocked",
	"geosite-category-ru",
	"geosite-ru-available-only-inside",
	"geoip-ru",
}

const (
	clashRoutingMaxBytes  = 1 << 20
	clashRuleKindMatch    = "MATCH"
	clashProviderHTTP     = "http"
	clashProviderInline   = "inline"
	clashBehaviorClassic  = "classical"
	clashFormatMRS        = "mrs"
	clashRoutingKeyRules  = "rules"
	clashRoutingProviders = "rule-providers"
	clashRoutingDNS       = "dns"
	clashRoutingSniffer   = "sniffer"
)

var clashRuleSetReference = regexp.MustCompile(`(?i)\bRULE-SET,\s*([^,()\s]+)`)

// ClashRouting is the routing an administrator adds to Mihomo profiles.
type ClashRouting struct {
	Rules         []string
	RuleProviders map[string]map[string]any
	DNS           map[string]any
	Sniffer       map[string]any
}

// HasMatch reports whether the rules end routing on their own, so they replace the built-in ones.
func (r ClashRouting) HasMatch() bool {
	return slices.ContainsFunc(r.Rules, func(rule string) bool {
		kind, _, _ := strings.Cut(rule, ",")

		return strings.EqualFold(strings.TrimSpace(kind), clashRuleKindMatch)
	})
}

// ClashRuleProviderNames returns the rule providers the rules refer to.
func ClashRuleProviderNames(rules []string) []string {
	var names []string

	for _, rule := range rules {
		for _, m := range clashRuleSetReference.FindAllStringSubmatch(rule, -1) {
			names = append(names, m[1])
		}
	}

	return names
}

// ParseClashRouting parses and checks the YAML an administrator saved. It accepts only what shapes routing, so
// the document cannot replace the proxies of a user, open a controller port or point at files on the client.
// It does not run Mihomo: a rule of an unknown kind, or GEOSITE on a client without such a category, passes here.
func ParseClashRouting(raw string) (ClashRouting, error) {
	if len(raw) > clashRoutingMaxBytes {
		return ClashRouting{}, invalidClashRouting("the document is too large: the limit is %d bytes", clashRoutingMaxBytes)
	}

	var doc map[string]any
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return ClashRouting{}, invalidClashRouting("not valid YAML with top-level keys: %v", err)
	}

	var (
		r   ClashRouting
		err error
	)

	for _, key := range sortedKeys(doc) {
		switch key {
		case clashRoutingKeyRules:
			r.Rules, err = parseClashRules(doc[key])
		case clashRoutingProviders:
			r.RuleProviders, err = parseClashProviders(doc[key])
		case clashRoutingDNS:
			r.DNS, err = clashMapping(key, doc[key])
		case clashRoutingSniffer:
			r.Sniffer, err = clashMapping(key, doc[key])
		default:
			err = invalidClashRouting("%q is not allowed: use rules, rule-providers, dns or sniffer", key)
		}

		if err != nil {
			return ClashRouting{}, err
		}
	}

	if len(r.Rules) == 0 && len(r.RuleProviders) == 0 && r.DNS == nil && r.Sniffer == nil {
		return ClashRouting{}, invalidClashRouting("set at least one of rules, rule-providers, dns or sniffer")
	}

	for _, name := range ClashRuleProviderNames(r.Rules) {
		if _, own := r.RuleProviders[name]; !own && !slices.Contains(ClashBuiltinRuleSets, name) {
			return ClashRouting{}, invalidClashRouting("rule-provider %q is not defined", name)
		}
	}

	return r, nil
}

func parseClashRules(value any) ([]string, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, invalidClashRouting("rules must be a list")
	}

	rules := make([]string, 0, len(items))

	for _, item := range items {
		rule, ok := item.(string)
		if !ok {
			return nil, invalidClashRouting("rule %v must be a string", item)
		}

		rule = strings.TrimSpace(rule)
		if err := checkClashRule(rule); err != nil {
			return nil, err
		}

		rules = append(rules, rule)
	}

	return rules, nil
}

func checkClashRule(rule string) error {
	parts, err := splitClashRule(rule)
	if err != nil {
		return err
	}

	kind := strings.ToUpper(parts[0])
	targetIndex := 2

	switch {
	case kind == "SUB-RULE":
		return invalidClashRouting("rule %q: SUB-RULE is not supported", rule)
	case kind == clashRuleKindMatch:
		if len(parts) != 2 {
			return invalidClashRouting("rule %q: expected MATCH,target", rule)
		}

		targetIndex = 1
	case len(parts) < 3:
		return invalidClashRouting("rule %q: expected type,value,target", rule)
	}

	if !isClashRuleTarget(parts[targetIndex]) {
		return invalidClashRouting("rule %q: unknown target %q: use proxy, auto, DIRECT, REJECT, REJECT-DROP or PASS",
			rule, parts[targetIndex])
	}

	return nil
}

// splitClashRule splits at commas outside parentheses, because logic rules nest whole rules.
func splitClashRule(rule string) ([]string, error) {
	var (
		parts []string
		start int
		depth int
	)

	for i, c := range rule {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(rule[start:i]))
				start = i + 1
			}
		}

		if depth < 0 {
			break
		}
	}

	if depth != 0 {
		return nil, invalidClashRouting("rule %q: unbalanced parentheses", rule)
	}

	return append(parts, strings.TrimSpace(rule[start:])), nil
}

func isClashRuleTarget(target string) bool {
	switch target {
	case ClashGroupProxy, ClashGroupAuto, "DIRECT", "REJECT", "REJECT-DROP", "PASS":
		return true
	}

	return false
}

func parseClashProviders(value any) (map[string]map[string]any, error) {
	providers, ok := value.(map[string]any)
	if !ok {
		return nil, invalidClashRouting("rule-providers must be a mapping")
	}

	out := make(map[string]map[string]any, len(providers))

	for _, name := range sortedKeys(providers) {
		if slices.Contains(ClashBuiltinRuleSets, name) {
			return nil, invalidClashRouting("rule-provider %q is built-in: choose another name", name)
		}

		provider, ok := providers[name].(map[string]any)
		if !ok {
			return nil, invalidClashRouting("rule-provider %q must be a mapping", name)
		}

		if err := checkClashProvider(name, provider); err != nil {
			return nil, err
		}

		out[name] = provider
	}

	return out, nil
}

func checkClashProvider(name string, p map[string]any) error {
	for _, key := range sortedKeys(p) {
		switch key {
		case "type", "behavior", "format", "url", "interval", "proxy", "payload", "size-limit":
		default:
			return invalidClashRouting("rule-provider %q: %q is not allowed", name, key)
		}
	}

	format, _ := p["format"].(string)

	switch p["behavior"] {
	case "domain", "ipcidr":
	case clashBehaviorClassic:
		if format == clashFormatMRS {
			return invalidClashRouting("rule-provider %q: mrs supports only domain and ipcidr", name)
		}
	default:
		return invalidClashRouting("rule-provider %q: behavior must be domain, ipcidr or classical", name)
	}

	switch p["type"] {
	case clashProviderHTTP:
		return checkClashHTTPProvider(name, p, format)
	case clashProviderInline:
		if payload, ok := p["payload"].([]any); !ok || len(payload) == 0 {
			return invalidClashRouting("rule-provider %q: an inline provider needs a payload list", name)
		}

		return nil
	default:
		return invalidClashRouting("rule-provider %q: type must be http or inline", name)
	}
}

func checkClashHTTPProvider(name string, p map[string]any, format string) error {
	raw, _ := p["url"].(string)
	if u, err := url.Parse(raw); err != nil || u.Scheme != "https" || u.Host == "" {
		return invalidClashRouting("rule-provider %q: url must be an https URL", name)
	}

	switch format {
	case "", "yaml", "text", clashFormatMRS:
	default:
		return invalidClashRouting("rule-provider %q: format must be yaml, text or mrs", name)
	}

	if v, set := p["interval"]; set {
		if seconds, ok := v.(int); !ok || seconds < 0 {
			return invalidClashRouting("rule-provider %q: interval must be a number of seconds from 0", name)
		}
	}

	if v, set := p["proxy"]; set {
		if proxy, ok := v.(string); !ok || (proxy != ClashGroupProxy && proxy != ClashGroupAuto && proxy != "DIRECT") {
			return invalidClashRouting("rule-provider %q: proxy must be proxy, auto or DIRECT", name)
		}
	}

	return nil
}

func clashMapping(key string, value any) (map[string]any, error) {
	m, ok := value.(map[string]any)
	if !ok {
		return nil, invalidClashRouting("%s must be a mapping", key)
	}

	return m, nil
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

func invalidClashRouting(format string, args ...any) error {
	return fmt.Errorf("%w: Mihomo routing: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
