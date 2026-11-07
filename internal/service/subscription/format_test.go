package subscription

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	yaml "gopkg.in/yaml.v3"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/singbox"
)

var update = flag.Bool("update", false, "rewrite golden files")

type nodeFile struct {
	Node  protocol.Node   `yaml:"node"`
	Users []protocol.User `yaml:"users"`
}

func loadNodeFile(t *testing.T, name string) nodeFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var f nodeFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// fixture has two nodes (n1: Reality, Hysteria2 with a self-signed certificate and obfs, WireGuard;
// n2: Reality, Hysteria2 with ACME) and users alice and bob; bob has no WG keys.
func fixture(t *testing.T, name string) Profile {
	t.Helper()
	n1, n2 := loadNodeFile(t, "n1.yaml"), loadNodeFile(t, "n2.yaml")
	var su protocol.User
	for _, u := range n1.Users {
		if u.Name == name {
			su = u
		}
	}
	if su.Name == "" {
		t.Fatalf("no user %s in fixture", name)
	}
	if name == "bob" {
		su.WG = nil
	}
	exp := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	return Profile{
		User: panel.User{ID: 1, Name: su.Name, UUID: su.UUID, Token: "tok", Enabled: true,
			TrafficLimitBytes: 100 << 30, ExpiresAt: &exp, WG: su.WG},
		Usage: panel.Usage{Up: 1 << 30, Down: 2 << 30},
		Nodes: []NodeProfile{
			{Node: panel.Node{ID: 1, Name: n1.Node.ID, Host: n1.Node.Host}, Inbounds: n1.Node.Inbounds},
			{Node: panel.Node{ID: 2, Name: n2.Node.ID, Host: n2.Node.Host}, Inbounds: n2.Node.Inbounds},
		},
	}
}

func checkGolden(t *testing.T, name string, got []byte) string {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs (run go test -update and review the diff)", path)
	}
	return path
}

// runTool runs an external config check if the binary is in PATH.
func runTool(t *testing.T, bin string, args ...string) {
	t.Helper()
	path, err := exec.LookPath(bin)
	if err != nil {
		t.Logf("%s not in PATH, skipping", bin)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, path, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", bin, args, err, out)
	}
}

func TestLinksGolden(t *testing.T) {
	for _, name := range []string{"alice", "bob"} {
		t.Run(name, func(t *testing.T) {
			links, err := Links(fixture(t, name))
			if err != nil {
				t.Fatal(err)
			}
			checkGolden(t, name+".links.txt", []byte(strings.Join(links, "\n")+"\n"))
		})
	}
}

func TestLinksContent(t *testing.T) {
	p := fixture(t, "alice")
	links, err := Links(p)
	if err != nil {
		t.Fatal(err)
	}
	// n1: vless, hy2, wg; n2: vless, hy2.
	if len(links) != 5 {
		t.Fatalf("got %d links: %v", len(links), links)
	}
	n1 := p.Nodes[0].Inbounds
	pbk, _ := protocol.RealityPublicKey(n1[0].Reality.PrivateKey)
	pin, _ := singbox.CertSHA256(n1[1].Hysteria2.CertPEM)
	wgPub, _ := protocol.WGPublicKey(n1[2].WireGuard.PrivateKey)

	tests := []struct {
		link     string
		scheme   string
		user     string
		hostport string
		params   map[string]string
		fragment string
	}{
		{links[0], "vless", p.User.UUID, "n1.example.com:443", map[string]string{
			"security": "reality", "pbk": pbk, "sid": n1[0].Reality.ShortIDs[0], "sni": "www.microsoft.com",
			"flow": "xtls-rprx-vision", "fp": "chrome", "type": "tcp",
		}, "n1 · Reality"},
		{links[1], "hysteria2", p.User.UUID, "n1.example.com:8443", map[string]string{
			"sni": "n1.example.com", "insecure": "1", "pinSHA256": pin, "obfs": "salamander", "obfs-password": "obfs-secret",
		}, "n1 · Hysteria2"},
		{links[2], "wireguard", p.User.WG.PrivateKey, "n1.example.com:51820", map[string]string{
			"publickey": wgPub, "presharedkey": p.User.WG.PresharedKey, "address": p.User.WG.Address, "mtu": "1408",
		}, "n1 · WireGuard"},
		{links[4], "hysteria2", p.User.UUID, "n2.example.com:443", map[string]string{"sni": "n2.example.com", "insecure": ""}, "n2 · Hysteria2"},
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.link)
		if err != nil {
			t.Fatalf("%s: %v", tt.link, err)
		}
		if u.Scheme != tt.scheme || u.User.Username() != tt.user || u.Host != tt.hostport || u.Fragment != tt.fragment {
			t.Errorf("%s: scheme %s user %s host %s fragment %q", tt.link, u.Scheme, u.User.Username(), u.Host, u.Fragment)
		}
		q := u.Query()
		for k, v := range tt.params {
			if q.Get(k) != v {
				t.Errorf("%s: %s = %q, want %q", tt.scheme, k, q.Get(k), v)
			}
		}
	}

	// bob without WG keys gets no WireGuard.
	bobLinks, _ := Links(fixture(t, "bob"))
	for _, l := range bobLinks {
		if strings.HasPrefix(l, "wireguard://") {
			t.Errorf("bob got wireguard link: %s", l)
		}
	}

	body, err := LinksBase64(p)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(body))
	if err != nil || string(decoded) != strings.Join(links, "\n") {
		t.Errorf("base64 body does not decode to links: %v", err)
	}
}

func TestSingBoxJSONGolden(t *testing.T) {
	got, err := SingBoxJSON(fixture(t, "alice"))
	if err != nil {
		t.Fatal(err)
	}
	path := checkGolden(t, "alice.singbox.json", got)
	runTool(t, "sing-box", "check", "-c", path)
	// Additional sing-box versions (e.g. 1.12) via SINGBOX_EXTRA=/path/a:/path/b.
	for _, bin := range filepath.SplitList(os.Getenv("SINGBOX_EXTRA")) {
		runTool(t, bin, "check", "-c", path)
	}
}

func TestSingBoxJSONNoServers(t *testing.T) {
	p := fixture(t, "alice")
	p.Nodes = nil
	got, err := SingBoxJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(path, got, 0o644); err != nil {
		t.Fatal(err)
	}
	runTool(t, "sing-box", "check", "-c", path)
}

func TestClashYAMLGolden(t *testing.T) {
	got, err := ClashYAML(fixture(t, "alice"))
	if err != nil {
		t.Fatal(err)
	}
	path := checkGolden(t, "alice.clash.yaml", got)
	var parsed map[string]any
	if err := yaml.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	if n := len(parsed["proxies"].([]any)); n != 5 {
		t.Errorf("%d proxies, want 5", n)
	}
	// Only with an explicit MIHOMO_CHECK=1, because it needs mihomo in PATH.
	if os.Getenv("MIHOMO_CHECK") == "1" {
		runTool(t, "mihomo", "-t", "-d", t.TempDir(), "-f", path)
	}
}

// The routing must not depend on the geodata files of the client: FlClash and Clash Verge Rev replace them with
// files that lack ru-blocked, and a profile with a GEOSITE rule on it does not start.
func TestClashYAMLRouting(t *testing.T) {
	got, err := ClashYAML(fixture(t, "alice"))
	if err != nil {
		t.Fatal(err)
	}

	var parsed struct {
		Rules         []string                  `yaml:"rules"`
		RuleProviders map[string]map[string]any `yaml:"rule-providers"`
	}
	if err := yaml.Unmarshal(got, &parsed); err != nil {
		t.Fatal(err)
	}

	wantRules := []string{
		"IP-CIDR,10.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,100.64.0.0/10,DIRECT,no-resolve",
		"IP-CIDR,127.0.0.0/8,DIRECT,no-resolve",
		"IP-CIDR,169.254.0.0/16,DIRECT,no-resolve",
		"IP-CIDR,172.16.0.0/12,DIRECT,no-resolve",
		"IP-CIDR,192.168.0.0/16,DIRECT,no-resolve",
		"IP-CIDR6,fc00::/7,DIRECT,no-resolve",
		"IP-CIDR6,fe80::/10,DIRECT,no-resolve",
		"RULE-SET,geosite-ru-blocked,proxy",
		"RULE-SET,geoip-ru-blocked,proxy,no-resolve",
		"DOMAIN-SUFFIX,ru,DIRECT",
		"DOMAIN-SUFFIX,su,DIRECT",
		"DOMAIN-SUFFIX,xn--p1ai,DIRECT",
		"RULE-SET,geosite-category-ru,DIRECT",
		"RULE-SET,geosite-ru-available-only-inside,DIRECT",
		"RULE-SET,geoip-ru,DIRECT",
		"MATCH,proxy",
	}
	if !slices.Equal(parsed.Rules, wantRules) {
		t.Errorf("rules:\n%s\nwant:\n%s", strings.Join(parsed.Rules, "\n"), strings.Join(wantRules, "\n"))
	}

	const base = "https://github.com/lavr-art/giocondo/releases/download/rules/"

	wantBehavior := map[string]string{
		"geosite-ru-blocked":               "domain",
		"geoip-ru-blocked":                 "ipcidr",
		"geosite-category-ru":              "domain",
		"geosite-ru-available-only-inside": "domain",
		"geoip-ru":                         "ipcidr",
	}
	if len(parsed.RuleProviders) != len(wantBehavior) {
		t.Errorf("%d rule providers, want %d", len(parsed.RuleProviders), len(wantBehavior))
	}

	for name, behavior := range wantBehavior {
		want := map[string]any{
			"type": "http", "behavior": behavior, "format": "mrs", "url": base + name + ".mrs", "interval": 86400, "proxy": "proxy",
		}
		if got := parsed.RuleProviders[name]; !maps.Equal(got, want) {
			t.Errorf("provider %s = %v, want %v", name, got, want)
		}
	}

	for _, banned := range []string{"GEOSITE", "GEOIP", "geox-url", "geodata-mode", "geodata-loader", "geo-auto-update"} {
		if strings.Contains(string(got), banned) {
			t.Errorf("the profile depends on client geodata: it contains %q", banned)
		}
	}
}

func TestClashYAMLCustomRouting(t *testing.T) {
	const custom = "rule-providers:\n  mine: {type: http, behavior: domain, format: text, url: 'https://example.com/l.txt', proxy: proxy}\n"

	builtinRules := len(clashYAMLParts(t, fixture(t, "alice")).Rules)

	tests := []struct {
		name          string
		enabled       bool
		yaml          string
		wantFirstRule string
		wantRules     int
		wantProviders []string
	}{
		{
			name: "a saved draft does not apply", enabled: false, yaml: "rules:\n  - MATCH,DIRECT\n",
			wantFirstRule: "IP-CIDR,10.0.0.0/8,DIRECT,no-resolve", wantRules: builtinRules,
			wantProviders: []string{"geoip-ru", "geoip-ru-blocked", "geosite-category-ru", "geosite-ru-available-only-inside", "geosite-ru-blocked"},
		},
		{
			name: "rules without MATCH go before the built-in ones", enabled: true,
			yaml:          custom + "rules:\n  - RULE-SET,mine,DIRECT\n",
			wantFirstRule: "RULE-SET,mine,DIRECT", wantRules: builtinRules + 1,
			wantProviders: []string{"geoip-ru", "geoip-ru-blocked", "geosite-category-ru", "geosite-ru-available-only-inside", "geosite-ru-blocked", "mine"},
		},
		{
			name: "rules with MATCH replace the built-in ones and drop their lists", enabled: true,
			yaml:          custom + "rules:\n  - RULE-SET,mine,DIRECT\n  - MATCH,proxy\n",
			wantFirstRule: "RULE-SET,mine,DIRECT", wantRules: 2,
			wantProviders: []string{"mine"},
		},
		{
			name: "a replacement keeps the built-in list it uses", enabled: true,
			yaml:          "rules:\n  - RULE-SET,geoip-ru,DIRECT\n  - MATCH,proxy\n",
			wantFirstRule: "RULE-SET,geoip-ru,DIRECT", wantRules: 2,
			wantProviders: []string{"geoip-ru"},
		},
		{
			name: "dns alone keeps the built-in rules", enabled: true, yaml: "dns:\n  enable: true\n",
			wantFirstRule: "IP-CIDR,10.0.0.0/8,DIRECT,no-resolve", wantRules: builtinRules,
			wantProviders: []string{"geoip-ru", "geoip-ru-blocked", "geosite-category-ru", "geosite-ru-available-only-inside", "geosite-ru-blocked"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := fixture(t, "alice")
			p.Settings.ClashRoutingEnabled = tt.enabled
			p.Settings.ClashRoutingYAML = tt.yaml

			parts := clashYAMLParts(t, p)
			if parts.Rules[0] != tt.wantFirstRule || len(parts.Rules) != tt.wantRules {
				t.Errorf("first rule %q of %d, want %q of %d", parts.Rules[0], len(parts.Rules), tt.wantFirstRule, tt.wantRules)
			}

			if got := slices.Sorted(maps.Keys(parts.RuleProviders)); !slices.Equal(got, tt.wantProviders) {
				t.Errorf("providers %v, want %v", got, tt.wantProviders)
			}

			if len(parts.Proxies) != 5 {
				t.Errorf("%d proxies, custom routing must not touch them", len(parts.Proxies))
			}

			if os.Getenv("MIHOMO_CHECK") == "1" {
				body, err := ClashYAML(p)
				if err != nil {
					t.Fatal(err)
				}

				path := filepath.Join(t.TempDir(), "custom.yaml")
				if err := os.WriteFile(path, body, 0o600); err != nil {
					t.Fatal(err)
				}

				runTool(t, "mihomo", "-t", "-d", t.TempDir(), "-f", path)
			}
		})
	}
}

func TestClashYAMLCustomDNSAndSniffer(t *testing.T) {
	p := fixture(t, "alice")
	p.Settings.ClashRoutingEnabled = true
	p.Settings.ClashRoutingYAML = "dns:\n  enable: true\n  ipv6: false\nsniffer:\n  enable: true\n"

	parts := clashYAMLParts(t, p)
	if parts.DNS["enable"] != true || parts.DNS["ipv6"] != false || parts.Sniffer["enable"] != true {
		t.Fatalf("dns = %v, sniffer = %v", parts.DNS, parts.Sniffer)
	}
}

// Without custom routing the profile leaves DNS and sniffing to the client, as before.
func TestClashYAMLHasNoDNSByDefault(t *testing.T) {
	parts := clashYAMLParts(t, fixture(t, "alice"))
	if parts.DNS != nil || parts.Sniffer != nil {
		t.Fatalf("dns = %v, sniffer = %v", parts.DNS, parts.Sniffer)
	}
}

func TestClashYAMLRejectsBrokenCustomRouting(t *testing.T) {
	p := fixture(t, "alice")
	p.Settings.ClashRoutingEnabled = true
	p.Settings.ClashRoutingYAML = "proxies: []\n"

	if _, err := ClashYAML(p); !errors.Is(err, panel.ErrInvalid) {
		t.Fatalf("ClashYAML() = %v, want ErrInvalid", err)
	}
}

// The model checks references to built-in lists without importing this package.
func TestBuiltinRuleSetsMatchTheModel(t *testing.T) {
	if got := tags(allRuleSets...); !slices.Equal(got, panel.ClashBuiltinRuleSets) {
		t.Fatalf("rule sets %v, model %v", got, panel.ClashBuiltinRuleSets)
	}
}

type clashParts struct {
	Proxies       []map[string]any          `yaml:"proxies"`
	Rules         []string                  `yaml:"rules"`
	RuleProviders map[string]map[string]any `yaml:"rule-providers"`
	DNS           map[string]any            `yaml:"dns"`
	Sniffer       map[string]any            `yaml:"sniffer"`
}

func clashYAMLParts(t *testing.T, p Profile) clashParts {
	t.Helper()

	body, err := ClashYAML(p)
	if err != nil {
		t.Fatal(err)
	}

	var parts clashParts
	if err := yaml.Unmarshal(body, &parts); err != nil {
		t.Fatal(err)
	}

	return parts
}

// The lists are published by deploy/rules/build.sh, so a set added here without it would be a 404 for clients.
func TestRuleSetsArePublished(t *testing.T) {
	script, err := os.ReadFile("../../../deploy/rules/build.sh")
	if err != nil {
		t.Fatal(err)
	}

	for _, rs := range allRuleSets {
		want := strings.TrimSuffix(rs.Path, ".srs")
		if !strings.Contains(string(script), rs.Behavior+" "+want) {
			t.Errorf("deploy/rules/build.sh does not publish %s %s", rs.Behavior, want)
		}
	}
}

func TestServerNamesUnique(t *testing.T) {
	p := fixture(t, "alice")
	extra := p.Nodes[0].Inbounds[0]
	extra.Tag, extra.Port = "vless2", 2443
	p.Nodes[0].Inbounds = append(p.Nodes[0].Inbounds, extra)
	srvs, err := servers(p)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range srvs {
		if seen[s.Name] {
			t.Errorf("duplicate server name %q", s.Name)
		}
		seen[s.Name] = true
	}
	if !seen["n1 · Reality (vless2)"] {
		t.Errorf("names: %v", seen)
	}
}

func TestStatusAndUserInfo(t *testing.T) {
	now := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	p := fixture(t, "alice")
	if s := p.Status(now); s != "" {
		t.Errorf("active user status %q", s)
	}
	if got, want := p.UserInfo(), "upload=1073741824; download=2147483648; total=107374182400; expire=1798761600"; got != want {
		t.Errorf("userinfo %q, want %q", got, want)
	}

	cases := map[string]func(*Profile){
		"disabled":              func(p *Profile) { p.User.Enabled = false },
		"expired":               func(p *Profile) { e := now; p.User.ExpiresAt = &e },
		"traffic limit reached": func(p *Profile) { p.Usage = panel.Usage{Up: 50 << 30, Down: 50 << 30} },
	}
	for want, mutate := range cases {
		q := fixture(t, "alice")
		mutate(&q)
		if got := q.Status(now); got != want {
			t.Errorf("status %q, want %q", got, want)
		}
	}

	q := fixture(t, "alice")
	q.User.TrafficLimitBytes, q.User.ExpiresAt = 0, nil
	q.Usage = panel.Usage{Up: 1 << 50}
	if q.Status(now) != "" || !strings.HasSuffix(q.UserInfo(), "total=0; expire=0") {
		t.Errorf("unlimited: status %q, userinfo %q", q.Status(now), q.UserInfo())
	}
}

// Host overrides: Reality has two addresses (two servers), Hysteria2 one address with another port and SNI,
// WireGuard without overrides stays on the node host.
func withHosts(p Profile) Profile {
	p.Nodes[0].Hosts = map[string][]panel.Host{
		"vless": {{Address: "203.0.113.10"}, {Address: "relay.example.net", Port: 2443, SNI: "ignored.example"}},
		"hy2":   {{Address: "198.51.100.7", Port: 9443, SNI: "cdn.example.net"}},
	}
	return p
}

func TestHostsGolden(t *testing.T) {
	p := withHosts(fixture(t, "alice"))
	links, err := Links(p)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "alice-hosts.links.txt", []byte(strings.Join(links, "\n")+"\n"))
	js, err := SingBoxJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	path := checkGolden(t, "alice-hosts.singbox.json", js)
	runTool(t, "sing-box", "check", "-c", path)
	ya, err := ClashYAML(p)
	if err != nil {
		t.Fatal(err)
	}
	checkGolden(t, "alice-hosts.clash.yaml", ya)
}

func TestHostsReplaceNodeAddress(t *testing.T) {
	p := withHosts(fixture(t, "alice"))
	srvs, err := servers(p)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]server{}
	for _, s := range srvs {
		got[s.Name] = s
	}
	r1, r2 := got["n1 · Reality · 203.0.113.10"], got["n1 · Reality · relay.example.net"]
	if r1.Host != "203.0.113.10" || r1.Port != 443 || r2.Host != "relay.example.net" || r2.Port != 2443 {
		t.Errorf("reality targets: %+v %+v", r1, r2)
	}
	if r2.SNI != "www.microsoft.com" {
		t.Errorf("reality SNI must stay server_name, got %q", r2.SNI)
	}
	hy := got["n1 · Hysteria2"]
	if hy.Host != "198.51.100.7" || hy.Port != 9443 || hy.SNI != "cdn.example.net" {
		t.Errorf("hysteria2 target %+v", hy)
	}
	if wg := got["n1 · WireGuard"]; wg.Host != "n1.example.com" {
		t.Errorf("wireguard without hosts must use node host: %+v", wg)
	}
	for name := range got {
		if strings.Contains(name, "n1.example.com") && strings.HasPrefix(name, "n1 · Reality") {
			t.Errorf("node host leaked into reality servers: %s", name)
		}
	}
	if len(srvs) != 6 { // n1: 2 reality + hy2 + wg; n2: reality + hy2
		t.Errorf("%d servers", len(srvs))
	}
}
