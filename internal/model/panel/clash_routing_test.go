package panel

import (
	"errors"
	"strings"
	"testing"
)

func TestParseClashRouting(t *testing.T) {
	const provider = "rule-providers:\n  mine: {type: http, behavior: domain, url: 'https://example.com/l.txt', format: text}\n"

	valid := []struct{ name, raw string }{
		{"rules only", "rules:\n  - DOMAIN-SUFFIX,example.com,DIRECT\n"},
		{"all targets", "rules:\n  - DOMAIN,a.example,proxy\n  - DOMAIN,b.example,auto\n  - DOMAIN,c.example,DIRECT\n" +
			"  - DOMAIN,d.example,REJECT\n  - DOMAIN,e.example,REJECT-DROP\n  - DOMAIN,f.example,PASS\n  - MATCH,proxy\n"},
		{"rule options", "rules:\n  - IP-CIDR,192.0.2.0/24,DIRECT,no-resolve\n"},
		{"own provider", provider + "rules:\n  - RULE-SET,mine,DIRECT\n"},
		{"built-in provider", "rules:\n  - RULE-SET,geoip-ru,DIRECT\n"},
		{"logic rule", "rules:\n  - AND,((DOMAIN,a.example),(NETWORK,UDP)),REJECT\n"},
		{"inline provider", "rule-providers:\n  mine: {type: inline, behavior: domain, payload: [a.example]}\n" +
			"rules:\n  - RULE-SET,mine,DIRECT\n"},
		{"provider through the VPN", "rule-providers:\n  mine: {type: http, behavior: ipcidr, format: mrs, " +
			"url: 'https://example.com/l.mrs', proxy: proxy, interval: 3600}\nrules:\n  - RULE-SET,mine,DIRECT\n"},
		{"dns and sniffer", "dns:\n  enable: true\nsniffer:\n  enable: true\n"},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseClashRouting(tt.raw); err != nil {
				t.Fatalf("ParseClashRouting() = %v", err)
			}
		})
	}

	invalid := []struct{ name, raw, want string }{
		{"not YAML", "rules: [", "not valid YAML"},
		{"a list instead of a mapping", "- MATCH,proxy\n", "not valid YAML"},
		{"nothing to apply", "rules: []\n", "at least one"},
		{"proxies are not allowed", "proxies: []\nrules:\n  - MATCH,proxy\n", `"proxies" is not allowed`},
		{"controller is not allowed", "external-controller: 0.0.0.0:9090\n", `"external-controller" is not allowed`},
		{"rules are not a list", "rules: MATCH,proxy\n", "rules must be a list"},
		{"rule is not a string", "rules:\n  - {a: b}\n", "must be a string"},
		{"rule without a target", "rules:\n  - DOMAIN,example.com\n", "DOMAIN,example.com"},
		{"MATCH with a payload", "rules:\n  - MATCH,x,proxy\n", "MATCH,x,proxy"},
		{"unknown target", "rules:\n  - DOMAIN,example.com,n1 · Reality\n", "unknown target"},
		{"unbalanced logic rule", "rules:\n  - AND,((DOMAIN,a.example),REJECT\n", "unbalanced"},
		{"sub-rules are not supported", "rules:\n  - SUB-RULE,(NETWORK,TCP),sub\n", "SUB-RULE"},
		{"unknown provider", "rules:\n  - RULE-SET,missing,DIRECT\n", `provider "missing"`},
		{"unknown provider in a logic rule", "rules:\n  - AND,((RULE-SET,missing),(NETWORK,UDP)),DIRECT\n", `provider "missing"`},
		{"provider overrides a built-in one", "rule-providers:\n  geoip-ru: {type: inline, behavior: ipcidr, payload: [192.0.2.0/24]}\n", "built-in"},
		{"file provider", "rule-providers:\n  mine: {type: file, behavior: domain, path: /etc/passwd}\n", `"path" is not allowed`},
		{"file provider type", "rule-providers:\n  mine: {type: file, behavior: domain}\n", "type must be http or inline"},
		{"provider without a URL", "rule-providers:\n  mine: {type: http, behavior: domain}\n", "https URL"},
		{"plain HTTP provider", "rule-providers:\n  mine: {type: http, behavior: domain, url: 'http://example.com/l'}\n", "https URL"},
		{"inline provider without a payload", "rule-providers:\n  mine: {type: inline, behavior: domain}\n", "payload"},
		{"unknown behavior", "rule-providers:\n  mine: {type: inline, behavior: nope, payload: [a]}\n", "behavior"},
		{"unknown format", "rule-providers:\n  mine: {type: http, behavior: domain, format: dat, url: 'https://e.example/l'}\n", "format"},
		{"classical mrs", "rule-providers:\n  mine: {type: http, behavior: classical, format: mrs, url: 'https://e.example/l'}\n", "mrs"},
		{"negative interval", "rule-providers:\n  mine: {type: http, behavior: domain, url: 'https://e.example/l', interval: -1}\n", "interval"},
		{"provider through an unknown proxy", "rule-providers:\n  mine: {type: http, behavior: domain, url: 'https://e.example/l', proxy: n1}\n", "proxy"},
		{"dns is not a mapping", "dns: on\n", "dns must be a mapping"},
		{"over the size limit", "rules:\n  - DOMAIN,example.com,DIRECT\n#" + strings.Repeat("x", 1<<20), "too large"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseClashRouting(tt.raw)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParseClashRouting() = %v, want ErrInvalid with %q", err, tt.want)
			}
		})
	}
}

func TestSubscriptionSettingsValidateClashRouting(t *testing.T) {
	s := DefaultSubscriptionSettings()

	s.ClashRoutingEnabled = true
	if err := s.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("enabled without rules: %v, want ErrInvalid", err)
	}

	s.ClashRoutingYAML = "rules:\n  - MATCH,proxy\n"
	if err := s.Validate(); err != nil {
		t.Fatalf("enabled with rules: %v", err)
	}

	// A draft is checked too: otherwise enabling it later would be the first time the error shows.
	s.ClashRoutingEnabled = false
	s.ClashRoutingYAML = "proxies: []\n"
	if err := s.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("disabled with invalid rules: %v, want ErrInvalid", err)
	}
}
