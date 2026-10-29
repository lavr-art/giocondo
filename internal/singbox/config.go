package singbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Default values for Options.
const (
	DefaultV2RayAPIListen = "127.0.0.1:10085"
	DefaultACMEDataDir    = "/var/lib/giocondo-vpn/sing-box/acme"
	DefaultLogLevel       = "warn"
	DefaultWGMTU          = 1408

	directTag   = "direct"
	dnsLocalTag = "local"
	// WGOutboundPrefix is the outbound tag prefix used to count WireGuard peer traffic:
	// the WireGuard endpoint does not pass the user to v2ray_api statistics.
	WGOutboundPrefix = "wg-"
)

// Options are build settings not tied to a specific node.
type Options struct {
	V2RayAPIListen string // statistics gRPC address, loopback only
	ACMEDataDir    string
	LogLevel       string
}

func (o Options) withDefaults() Options {
	if o.V2RayAPIListen == "" {
		o.V2RayAPIListen = DefaultV2RayAPIListen
	}
	if o.ACMEDataDir == "" {
		o.ACMEDataDir = DefaultACMEDataDir
	}
	if o.LogLevel == "" {
		o.LogLevel = DefaultLogLevel
	}
	return o
}

// Config is the subset of the sing-box 1.14 config schema that we generate.
type Config struct {
	Log          *LogConfig    `json:"log,omitempty"`
	DNS          *DNSConfig    `json:"dns,omitempty"`
	Endpoints    []any         `json:"endpoints,omitempty"`
	Inbounds     []any         `json:"inbounds,omitempty"`
	Outbounds    []Outbound    `json:"outbounds"`
	Route        *Route        `json:"route,omitempty"`
	Experimental *Experimental `json:"experimental,omitempty"`
}

// LogConfig is the log section.
type LogConfig struct {
	Level     string `json:"level"`
	Timestamp bool   `json:"timestamp"`
}

// DNSConfig is the dns section.
type DNSConfig struct {
	Servers []DNSServer `json:"servers"`
}

// DNSServer is a DNS server in the new format (sing-box 1.12+).
type DNSServer struct {
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

// VLESSInbound — inbound vless.
type VLESSInbound struct {
	Type       string      `json:"type"`
	Tag        string      `json:"tag"`
	Listen     string      `json:"listen"`
	ListenPort uint16      `json:"listen_port"`
	Users      []VLESSUser `json:"users"`
	TLS        InboundTLS  `json:"tls"`
}

// VLESSUser is a vless user.
type VLESSUser struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
	Flow string `json:"flow"`
}

// Hysteria2Inbound — inbound hysteria2.
type Hysteria2Inbound struct {
	Type       string          `json:"type"`
	Tag        string          `json:"tag"`
	Listen     string          `json:"listen"`
	ListenPort uint16          `json:"listen_port"`
	UpMbps     int             `json:"up_mbps,omitempty"`
	DownMbps   int             `json:"down_mbps,omitempty"`
	Obfs       *Hysteria2Obfs  `json:"obfs,omitempty"`
	Users      []Hysteria2User `json:"users"`
	TLS        InboundTLS      `json:"tls"`
}

// Hysteria2Obfs is salamander obfuscation.
type Hysteria2Obfs struct {
	Type     string `json:"type"`
	Password string `json:"password"`
}

// Hysteria2User is a hysteria2 user.
type Hysteria2User struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// InboundTLS is server TLS.
type InboundTLS struct {
	Enabled             bool                 `json:"enabled"`
	ServerName          string               `json:"server_name,omitempty"`
	ALPN                []string             `json:"alpn,omitempty"`
	Certificate         []string             `json:"certificate,omitempty"`
	Key                 []string             `json:"key,omitempty"`
	CertificateProvider *CertificateProvider `json:"certificate_provider,omitempty"`
	Reality             *RealityTLS          `json:"reality,omitempty"`
}

// CertificateProvider is an inline certificate provider (ACME) replacing the deprecated tls.acme.
type CertificateProvider struct {
	Type          string   `json:"type"`
	Domain        []string `json:"domain"`
	DataDirectory string   `json:"data_directory,omitempty"`
	Email         string   `json:"email,omitempty"`
}

// RealityTLS is the tls.reality section.
type RealityTLS struct {
	Enabled    bool             `json:"enabled"`
	Handshake  RealityHandshake `json:"handshake"`
	PrivateKey string           `json:"private_key"`
	ShortID    []string         `json:"short_id"`
}

// RealityHandshake is the site Reality disguises itself as.
type RealityHandshake struct {
	Server     string `json:"server"`
	ServerPort uint16 `json:"server_port"`
}

// WireGuardEndpoint — endpoint wireguard.
type WireGuardEndpoint struct {
	Type       string          `json:"type"`
	Tag        string          `json:"tag"`
	System     bool            `json:"system"`
	MTU        int             `json:"mtu"`
	Address    []string        `json:"address"`
	PrivateKey string          `json:"private_key"`
	ListenPort uint16          `json:"listen_port"`
	Peers      []WireGuardPeer `json:"peers"`
}

// WireGuardPeer is a wireguard peer.
type WireGuardPeer struct {
	PublicKey    string   `json:"public_key"`
	PresharedKey string   `json:"pre_shared_key,omitempty"`
	AllowedIPs   []string `json:"allowed_ips"`
}

// Outbound is an outbound (we only use direct).
type Outbound struct {
	Type string `json:"type"`
	Tag  string `json:"tag"`
}

// Route is the route section.
type Route struct {
	Rules                 []RouteRule `json:"rules,omitempty"`
	Final                 string      `json:"final"`
	DefaultDomainResolver string      `json:"default_domain_resolver,omitempty"`
}

// RouteRule is a routing rule with an action.
type RouteRule struct {
	Inbound      []string `json:"inbound,omitempty"`
	SourceIPCIDR []string `json:"source_ip_cidr,omitempty"`
	IPIsPrivate  bool     `json:"ip_is_private,omitempty"`
	Action       string   `json:"action"`
	Outbound     string   `json:"outbound,omitempty"`
}

// Experimental is the experimental section.
type Experimental struct {
	V2RayAPI *V2RayAPI `json:"v2ray_api,omitempty"`
}

// V2RayAPI is the statistics gRPC API (requires a sing-box build with the with_v2ray_api tag).
type V2RayAPI struct {
	Listen string     `json:"listen"`
	Stats  V2RayStats `json:"stats"`
}

// V2RayStats selects which counters to keep.
type V2RayStats struct {
	Enabled   bool     `json:"enabled"`
	Outbounds []string `json:"outbounds,omitempty"`
	Users     []string `json:"users,omitempty"`
}

// Build builds the sing-box config for a node. Users are sorted by name,
// so the result does not depend on the input order.
func Build(n *Node, users []User, opts Options) (*Config, error) {
	if err := Validate(n, users); err != nil {
		return nil, err
	}
	opts = opts.withDefaults()

	users = append([]User(nil), users...)
	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })

	cfg := &Config{
		Log:       &LogConfig{Level: opts.LogLevel, Timestamp: true},
		DNS:       &DNSConfig{Servers: []DNSServer{{Type: "local", Tag: dnsLocalTag}}},
		Outbounds: []Outbound{{Type: "direct", Tag: directTag}},
		Route: &Route{
			Rules: []RouteRule{
				// Resolve the domain first, otherwise "localhost" and similar names bypass the block below.
				{Action: "resolve"},
				// Clients must not reach the node's local network or v2ray_api on loopback.
				{IPIsPrivate: true, Action: "reject"},
			},
			Final:                 directTag,
			DefaultDomainResolver: dnsLocalTag,
		},
	}

	statUsers := make([]string, 0, len(users))
	for _, u := range users {
		if !strings.HasPrefix(u.Name, "giocondo-vpn-probe-") {
			statUsers = append(statUsers, u.Name)
		}
	}
	var statOutbounds []string

	for _, in := range n.Inbounds {
		tag := in.EffectiveTag()
		switch in.Type {
		case TypeVLESSReality:
			cfg.Inbounds = append(cfg.Inbounds, buildVLESS(tag, in, users))
		case TypeHysteria2:
			cfg.Inbounds = append(cfg.Inbounds, buildHysteria2(tag, in, n.Host, users, opts))
		case TypeWireGuard:
			ep, err := buildWireGuard(tag, in, users)
			if err != nil {
				return nil, err
			}
			cfg.Endpoints = append(cfg.Endpoints, ep)
			for _, u := range users {
				out := WGOutboundPrefix + u.Name
				cfg.Outbounds = append(cfg.Outbounds, Outbound{Type: "direct", Tag: out})
				cfg.Route.Rules = append(cfg.Route.Rules, RouteRule{
					Inbound:      []string{tag},
					SourceIPCIDR: []string{u.WG.Address},
					Action:       "route",
					Outbound:     out,
				})
				if !strings.HasPrefix(u.Name, "giocondo-vpn-probe-") {
					statOutbounds = append(statOutbounds, out)
				}
			}
		}
	}

	cfg.Experimental = &Experimental{V2RayAPI: &V2RayAPI{
		Listen: opts.V2RayAPIListen,
		Stats:  V2RayStats{Enabled: true, Outbounds: statOutbounds, Users: statUsers},
	}}
	return cfg, nil
}

// Marshal serializes the config to indented JSON with a trailing newline.
func (c *Config) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildVLESS(tag string, in Inbound, users []User) VLESSInbound {
	p := in.Reality
	host, port, _ := splitDest(p.Dest) // checked in Validate
	vu := make([]VLESSUser, 0, len(users))
	for _, u := range users {
		vu = append(vu, VLESSUser{Name: u.Name, UUID: u.UUID, Flow: "xtls-rprx-vision"})
	}
	return VLESSInbound{
		Type:       "vless",
		Tag:        tag,
		Listen:     "::",
		ListenPort: in.Port,
		Users:      vu,
		TLS: InboundTLS{
			Enabled:    true,
			ServerName: p.ServerName,
			Reality: &RealityTLS{
				Enabled:    true,
				Handshake:  RealityHandshake{Server: host, ServerPort: port},
				PrivateKey: p.PrivateKey,
				ShortID:    p.ShortIDs,
			},
		},
	}
}

func buildHysteria2(tag string, in Inbound, host string, users []User, opts Options) Hysteria2Inbound {
	p := in.Hysteria2
	hu := make([]Hysteria2User, 0, len(users))
	for _, u := range users {
		hu = append(hu, Hysteria2User{Name: u.Name, Password: u.UUID})
	}
	tls := InboundTLS{Enabled: true, ALPN: []string{"h3"}}
	if p.TLSMode() == TLSACME {
		tls.ServerName = host
		tls.CertificateProvider = &CertificateProvider{
			Type:          "acme",
			Domain:        []string{host},
			DataDirectory: opts.ACMEDataDir,
			Email:         p.ACMEEmail,
		}
	} else {
		tls.Certificate = pemLines(p.CertPEM)
		tls.Key = pemLines(p.KeyPEM)
	}
	out := Hysteria2Inbound{
		Type:       "hysteria2",
		Tag:        tag,
		Listen:     "::",
		ListenPort: in.Port,
		UpMbps:     p.UpMbps,
		DownMbps:   p.DownMbps,
		Users:      hu,
		TLS:        tls,
	}
	if p.ObfsPassword != "" {
		out.Obfs = &Hysteria2Obfs{Type: "salamander", Password: p.ObfsPassword}
	}
	return out
}

func buildWireGuard(tag string, in Inbound, users []User) (WireGuardEndpoint, error) {
	p := in.WireGuard
	subnet, err := wgPrefix(p.WGSubnet())
	if err != nil {
		return WireGuardEndpoint{}, err
	}
	mtu := p.MTU
	if mtu == 0 {
		mtu = DefaultWGMTU
	}
	peers := make([]WireGuardPeer, 0, len(users))
	for _, u := range users {
		peers = append(peers, WireGuardPeer{
			PublicKey:    u.WG.PublicKey,
			PresharedKey: u.WG.PresharedKey,
			AllowedIPs:   []string{u.WG.Address},
		})
	}
	return WireGuardEndpoint{
		Type:       "wireguard",
		Tag:        tag,
		MTU:        mtu,
		Address:    []string{netip.PrefixFrom(serverWGAddr(subnet), subnet.Bits()).String()},
		PrivateKey: p.PrivateKey,
		ListenPort: in.Port,
		Peers:      peers,
	}, nil
}

// pemLines turns PEM into an array of lines, which is how sing-box accepts inline certificates.
func pemLines(s string) []string {
	return strings.Split(strings.TrimSpace(s), "\n")
}

// WireGuardClientConfig returns the user's wg-quick config for this node.
func WireGuardClientConfig(n *Node, u User) (string, error) {
	in := n.WireGuardInbound()
	if in == nil || in.WireGuard == nil {
		return "", fmt.Errorf("node %s has no wireguard inbound", n.ID)
	}
	if u.WG == nil {
		return "", fmt.Errorf("user %s has no wg peer", u.Name)
	}
	serverPub, err := WGPublicKey(in.WireGuard.PrivateKey)
	if err != nil {
		return "", err
	}
	mtu := in.WireGuard.MTU
	if mtu == 0 {
		mtu = DefaultWGMTU
	}
	host := n.Host
	if a, err := netip.ParseAddr(host); err == nil && a.Is6() {
		host = "[" + host + "]"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[Interface]\nPrivateKey = %s\nAddress = %s\nDNS = 1.1.1.1\nMTU = %d\n\n", u.WG.PrivateKey, u.WG.Address, mtu)
	fmt.Fprintf(&b, "[Peer]\nPublicKey = %s\nPresharedKey = %s\nEndpoint = %s:%d\nAllowedIPs = 0.0.0.0/0, ::/0\nPersistentKeepalive = 25\n",
		serverPub, u.WG.PresharedKey, host, in.Port)
	return b.String(), nil
}
