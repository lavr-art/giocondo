package subscription

import (
	"bytes"
	"encoding/json"

	"github.com/artemlavr/giocondo/internal/singbox"
)

// The client config targets sing-box 1.12+: the new DNS server format, rule actions,
// WireGuard as an endpoint. Checked with `sing-box check` on 1.12 and 1.14.

const (
	tagProxy  = "proxy"
	tagAuto   = "auto"
	tagDirect = "direct"
	tagDNSRem = "dns-remote"
	tagDNSLoc = "dns-local"

	urlTestURL = "https://www.gstatic.com/generate_204"
)

type clientConfig struct {
	Log          map[string]any `json:"log"`
	DNS          clientDNS      `json:"dns"`
	Inbounds     []any          `json:"inbounds"`
	Endpoints    []any          `json:"endpoints,omitempty"`
	Outbounds    []any          `json:"outbounds"`
	Route        clientRoute    `json:"route"`
	Experimental map[string]any `json:"experimental"`
}

type clientDNS struct {
	Servers  []map[string]any `json:"servers"`
	Rules    []map[string]any `json:"rules"`
	Final    string           `json:"final"`
	Strategy string           `json:"strategy"`
}

type clientRoute struct {
	Rules                 []map[string]any `json:"rules"`
	RuleSet               []map[string]any `json:"rule_set"`
	Final                 string           `json:"final"`
	AutoDetectInterface   bool             `json:"auto_detect_interface"`
	DefaultDomainResolver string           `json:"default_domain_resolver"`
}

type vlessOut struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Server     string `json:"server"`
	ServerPort uint16 `json:"server_port"`
	UUID       string `json:"uuid"`
	Flow       string `json:"flow"`
	TLS        tlsOut `json:"tls"`
}

type hy2Out struct {
	Type       string   `json:"type"`
	Tag        string   `json:"tag"`
	Server     string   `json:"server"`
	ServerPort uint16   `json:"server_port"`
	Password   string   `json:"password"`
	Obfs       *obfsOut `json:"obfs,omitempty"`
	TLS        tlsOut   `json:"tls"`
}

type obfsOut struct {
	Type     string `json:"type"`
	Password string `json:"password"`
}

type tlsOut struct {
	Enabled    bool        `json:"enabled"`
	ServerName string      `json:"server_name"`
	Insecure   bool        `json:"insecure,omitempty"`
	ALPN       []string    `json:"alpn,omitempty"`
	UTLS       *utlsOut    `json:"utls,omitempty"`
	Reality    *realityOut `json:"reality,omitempty"`
}

type utlsOut struct {
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint"`
}

type realityOut struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
	ShortID   string `json:"short_id"`
}

type wgEndpoint struct {
	Type       string   `json:"type"`
	Tag        string   `json:"tag"`
	Address    []string `json:"address"`
	PrivateKey string   `json:"private_key"`
	MTU        int      `json:"mtu"`
	Peers      []wgPeer `json:"peers"`
}

type wgPeer struct {
	Address      string   `json:"address"`
	Port         uint16   `json:"port"`
	PublicKey    string   `json:"public_key"`
	PresharedKey string   `json:"pre_shared_key,omitempty"`
	AllowedIPs   []string `json:"allowed_ips"`
}

// clientRuleSets are the runetfreedom remote rule-sets. They are downloaded through the VPN (GitHub is sometimes
// unreachable directly) and updated daily.
func clientRuleSets() []map[string]any {
	out := make([]map[string]any, 0, len(allRuleSets))
	for _, rs := range allRuleSets {
		out = append(out, map[string]any{
			"type": "remote", "tag": rs.Tag, "format": "binary", "url": rs.URL(),
			"download_detour": tagProxy, "update_interval": "1d",
		})
	}
	return out
}

// SingBoxJSON builds a full config for sing-box apps (SFA/SFI/SFM) and Karing:
// tun, DNS (remote through the VPN, local for RU), groups auto (url-test) and proxy (select),
// RU domains, RU IPs and private networks direct.
func SingBoxJSON(p Profile) ([]byte, error) {
	srvs, err := servers(p)
	if err != nil {
		return nil, err
	}
	cfg := clientConfig{
		Log: map[string]any{"level": "warn", "timestamp": true},
		DNS: clientDNS{
			Servers: []map[string]any{
				{"type": "https", "tag": tagDNSRem, "server": "1.1.1.1", "detour": tagProxy},
				{"type": "local", "tag": tagDNSLoc},
			},
			// Blocked domains are resolved through the VPN, Russian ones locally (the CDN returns
			// nearby addresses), the rest through the VPN.
			Rules: []map[string]any{
				{"rule_set": tags(rsSiteBlocked), "server": tagDNSRem},
				{"domain_suffix": directSuffixes, "server": tagDNSLoc},
				{"rule_set": tags(rsSiteRU, rsSiteInside), "server": tagDNSLoc},
			},
			Final:    tagDNSRem,
			Strategy: "prefer_ipv4",
		},
		Inbounds: []any{map[string]any{
			"type":         "tun",
			"tag":          "tun-in",
			"address":      []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"},
			"auto_route":   true,
			"strict_route": true,
			"stack":        "mixed",
		}},
		Route: clientRoute{
			Rules: []map[string]any{
				{"action": "sniff"},
				{"protocol": "dns", "action": "hijack-dns"},
				{"ip_is_private": true, "outbound": tagDirect},
				// Same order as in routing.go: blocked takes precedence over "Russian".
				{"rule_set": tags(rsSiteBlocked, rsIPBlocked), "outbound": tagProxy},
				{"domain_suffix": directSuffixes, "outbound": tagDirect},
				{"rule_set": tags(rsSiteRU, rsSiteInside, rsIPRU), "outbound": tagDirect},
			},
			RuleSet:               clientRuleSets(),
			Final:                 tagProxy,
			AutoDetectInterface:   true,
			DefaultDomainResolver: tagDNSLoc,
		},
		// cache_file remembers the server selected in the proxy group between runs.
		Experimental: map[string]any{"cache_file": map[string]any{"enabled": true}},
	}

	names := make([]string, 0, len(srvs))
	var outbounds []any
	for _, s := range srvs {
		names = append(names, s.Name)
		switch s.Type {
		case singbox.TypeVLESSReality:
			outbounds = append(outbounds, vlessOut{
				Type: "vless", Tag: s.Name, Server: s.Host, ServerPort: s.Port, UUID: s.UUID, Flow: "xtls-rprx-vision",
				TLS: tlsOut{
					Enabled: true, ServerName: s.SNI,
					UTLS:    &utlsOut{Enabled: true, Fingerprint: "chrome"},
					Reality: &realityOut{Enabled: true, PublicKey: s.PublicKey, ShortID: s.ShortID},
				},
			})
		case singbox.TypeHysteria2:
			out := hy2Out{
				Type: "hysteria2", Tag: s.Name, Server: s.Host, ServerPort: s.Port, Password: s.UUID,
				TLS: tlsOut{Enabled: true, ServerName: s.SNI, Insecure: s.Insecure, ALPN: []string{"h3"}},
			}
			if s.ObfsPassword != "" {
				out.Obfs = &obfsOut{Type: "salamander", Password: s.ObfsPassword}
			}
			outbounds = append(outbounds, out)
		case singbox.TypeWireGuard:
			cfg.Endpoints = append(cfg.Endpoints, wgEndpoint{
				Type: "wireguard", Tag: s.Name, Address: []string{s.WGAddress}, PrivateKey: s.WGPrivateKey, MTU: s.MTU,
				Peers: []wgPeer{{
					Address: s.Host, Port: s.Port, PublicKey: s.WGPeerPubKey, PresharedKey: s.WGPresharedKey,
					AllowedIPs: []string{"0.0.0.0/0", "::/0"},
				}},
			})
		}
	}

	// Without servers the groups would be empty and the config would fail the check: send everything direct.
	groupMembers := names
	if len(groupMembers) == 0 {
		groupMembers = []string{tagDirect}
	}
	cfg.Outbounds = append([]any{
		map[string]any{"type": "selector", "tag": tagProxy, "outbounds": append([]string{tagAuto}, groupMembers...), "default": tagAuto},
		map[string]any{"type": "urltest", "tag": tagAuto, "outbounds": groupMembers, "url": urlTestURL, "interval": "3m"},
	}, outbounds...)
	cfg.Outbounds = append(cfg.Outbounds, map[string]any{"type": "direct", "tag": tagDirect})

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
