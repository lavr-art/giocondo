package subscription

import (
	"bytes"
	"net/netip"

	"gopkg.in/yaml.v3"

	"github.com/artemlavr/giocondo/internal/singbox"
)

// privateCIDRs go direct, without resolving.
var privateCIDRs = []string{
	"10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16",
	"fc00::/7", "fe80::/10",
}

type clashConfig struct {
	MixedPort         int               `yaml:"mixed-port"`
	AllowLAN          bool              `yaml:"allow-lan"`
	Mode              string            `yaml:"mode"`
	LogLevel          string            `yaml:"log-level"`
	IPv6              bool              `yaml:"ipv6"`
	GeodataMode       bool              `yaml:"geodata-mode"`
	GeodataLoader     string            `yaml:"geodata-loader"`
	GeoAutoUpdate     bool              `yaml:"geo-auto-update"`
	GeoUpdateInterval int               `yaml:"geo-update-interval"` // hours
	GeoxURL           map[string]string `yaml:"geox-url"`
	Proxies           []clashProxy      `yaml:"proxies"`
	ProxyGroups       []clashGroup      `yaml:"proxy-groups"`
	Rules             []string          `yaml:"rules"`
}

// clashProxy is the union of Mihomo fields for vless, hysteria2 and wireguard; empty fields are omitted.
type clashProxy struct {
	Name   string `yaml:"name"`
	Type   string `yaml:"type"`
	Server string `yaml:"server"`
	Port   uint16 `yaml:"port"`
	UDP    bool   `yaml:"udp"`

	UUID              string        `yaml:"uuid,omitempty"`
	Network           string        `yaml:"network,omitempty"`
	TLS               bool          `yaml:"tls,omitempty"`
	Flow              string        `yaml:"flow,omitempty"`
	ServerName        string        `yaml:"servername,omitempty"`
	ClientFingerprint string        `yaml:"client-fingerprint,omitempty"`
	RealityOpts       *clashReality `yaml:"reality-opts,omitempty"`

	Password       string   `yaml:"password,omitempty"`
	SNI            string   `yaml:"sni,omitempty"`
	SkipCertVerify bool     `yaml:"skip-cert-verify,omitempty"`
	Fingerprint    string   `yaml:"fingerprint,omitempty"` // SHA-256 of the self-signed certificate
	ALPN           []string `yaml:"alpn,omitempty"`
	Obfs           string   `yaml:"obfs,omitempty"`
	ObfsPassword   string   `yaml:"obfs-password,omitempty"`

	IP           string   `yaml:"ip,omitempty"`
	PrivateKey   string   `yaml:"private-key,omitempty"`
	PublicKey    string   `yaml:"public-key,omitempty"`
	PreSharedKey string   `yaml:"pre-shared-key,omitempty"`
	AllowedIPs   []string `yaml:"allowed-ips,omitempty"`
	MTU          int      `yaml:"mtu,omitempty"`
}

type clashReality struct {
	PublicKey string `yaml:"public-key"`
	ShortID   string `yaml:"short-id"`
}

type clashGroup struct {
	Name     string   `yaml:"name"`
	Type     string   `yaml:"type"`
	Proxies  []string `yaml:"proxies"`
	URL      string   `yaml:"url,omitempty"`
	Interval int      `yaml:"interval,omitempty"`
}

// ClashYAML builds a config for Mihomo (Clash Verge, FlClash, Clash Meta): groups auto (url-test)
// and proxy (select), RU domains, RU IPs and private networks direct.
func ClashYAML(p Profile) ([]byte, error) {
	srvs, err := servers(p)
	if err != nil {
		return nil, err
	}
	cfg := clashConfig{
		MixedPort: 7890, Mode: "rule", LogLevel: "warning", IPv6: true,
		// runetfreedom categories (routing.go) in geodata format.
		GeodataMode: true, GeodataLoader: "memconservative", GeoAutoUpdate: true, GeoUpdateInterval: 72,
		GeoxURL: map[string]string{"geoip": geoipDatURL, "geosite": geositeDatURL},
	}
	names := make([]string, 0, len(srvs))
	for _, s := range srvs {
		names = append(names, s.Name)
		px := clashProxy{Name: s.Name, Server: s.Host, Port: s.Port, UDP: true}
		switch s.Type {
		case singbox.TypeVLESSReality:
			px.Type, px.UUID, px.Network, px.TLS, px.Flow = "vless", s.UUID, "tcp", true, "xtls-rprx-vision"
			px.ServerName, px.ClientFingerprint = s.SNI, "chrome"
			px.RealityOpts = &clashReality{PublicKey: s.PublicKey, ShortID: s.ShortID}
		case singbox.TypeHysteria2:
			px.Type, px.Password, px.SNI, px.ALPN = "hysteria2", s.UUID, s.SNI, []string{"h3"}
			if s.Insecure {
				// The certificate is self-signed: the CA is not verified, but its fingerprint is pinned.
				px.SkipCertVerify, px.Fingerprint = true, s.PinSHA256
			}
			if s.ObfsPassword != "" {
				px.Obfs, px.ObfsPassword = "salamander", s.ObfsPassword
			}
		case singbox.TypeWireGuard:
			px.Type, px.PrivateKey, px.PublicKey, px.PreSharedKey = "wireguard", s.WGPrivateKey, s.WGPeerPubKey, s.WGPresharedKey
			px.AllowedIPs, px.MTU = []string{"0.0.0.0/0", "::/0"}, s.MTU
			if a, err := netip.ParsePrefix(s.WGAddress); err == nil {
				px.IP = a.Addr().String()
			}
		}
		cfg.Proxies = append(cfg.Proxies, px)
	}

	members := names
	if len(members) == 0 {
		members = []string{"DIRECT"}
	}
	cfg.ProxyGroups = []clashGroup{
		{Name: tagProxy, Type: "select", Proxies: append([]string{tagAuto}, members...)},
		{Name: tagAuto, Type: "url-test", Proxies: members, URL: urlTestURL, Interval: 300},
	}
	for _, c := range privateCIDRs {
		kind := "IP-CIDR"
		if netip.MustParsePrefix(c).Addr().Is6() {
			kind = "IP-CIDR6"
		}
		cfg.Rules = append(cfg.Rules, kind+","+c+",DIRECT,no-resolve")
	}
	// Same order as in routing.go: blocked takes precedence over "Russian".
	cfg.Rules = append(cfg.Rules, "GEOSITE,ru-blocked,"+tagProxy, "GEOIP,ru-blocked,"+tagProxy+",no-resolve")
	for _, d := range directSuffixes {
		cfg.Rules = append(cfg.Rules, "DOMAIN-SUFFIX,"+d+",DIRECT")
	}
	cfg.Rules = append(cfg.Rules, "GEOSITE,category-ru,DIRECT", "GEOSITE,ru-available-only-inside,DIRECT",
		"GEOIP,ru,DIRECT", "MATCH,"+tagProxy)

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
