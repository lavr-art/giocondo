package singbox

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// InboundType is the inbound protocol on a node.
type InboundType string

// Supported protocols.
const (
	TypeVLESSReality InboundType = "vless-reality"
	TypeHysteria2    InboundType = "hysteria2"
	TypeWireGuard    InboundType = "wireguard"
)

// TLSMode is the certificate source for Hysteria2.
type TLSMode string

// Hysteria2 TLS modes.
const (
	TLSSelfSigned TLSMode = "self-signed"
	TLSACME       TLSMode = "acme"
)

// DefaultWGSubnet is the default WireGuard subnet. The server gets .1, clients start at .2.
const DefaultWGSubnet = "10.70.0.0/24"

// Node is a node and its inbound profiles.
type Node struct {
	ID       string    `yaml:"id" json:"id"`
	Host     string    `yaml:"host" json:"host"` // domain or IP clients connect to
	Inbounds []Inbound `yaml:"inbounds" json:"inbounds"`
}

// Inbound is one protocol on a node. Exactly one parameter block matching Type is set.
type Inbound struct {
	Type      InboundType      `yaml:"type" json:"type"`
	Tag       string           `yaml:"tag,omitempty" json:"tag,omitempty"`
	Port      uint16           `yaml:"port" json:"port"`
	Reality   *RealityParams   `yaml:"reality,omitempty" json:"reality,omitempty"`
	Hysteria2 *Hysteria2Params `yaml:"hysteria2,omitempty" json:"hysteria2,omitempty"`
	WireGuard *WireGuardParams `yaml:"wireguard,omitempty" json:"wireguard,omitempty"`
}

// RealityParams holds VLESS Reality parameters.
type RealityParams struct {
	PrivateKey string   `yaml:"private_key" json:"private_key"`
	ShortIDs   []string `yaml:"short_ids" json:"short_ids"`
	Dest       string   `yaml:"dest" json:"dest"`               // handshake site, host:port
	ServerName string   `yaml:"server_name" json:"server_name"` // SNI sent by the client
}

// Hysteria2Params holds Hysteria2 parameters. The user password is their UUID.
type Hysteria2Params struct {
	TLS          TLSMode `yaml:"tls,omitempty" json:"tls,omitempty"` // self-signed by default
	CertPEM      string  `yaml:"cert_pem,omitempty" json:"cert_pem,omitempty"`
	KeyPEM       string  `yaml:"key_pem,omitempty" json:"key_pem,omitempty"`
	ACMEEmail    string  `yaml:"acme_email,omitempty" json:"acme_email,omitempty"`
	ObfsPassword string  `yaml:"obfs_password,omitempty" json:"obfs_password,omitempty"` // salamander when set
	UpMbps       int     `yaml:"up_mbps,omitempty" json:"up_mbps,omitempty"`
	DownMbps     int     `yaml:"down_mbps,omitempty" json:"down_mbps,omitempty"`
}

// WireGuardParams is the WireGuard server side.
type WireGuardParams struct {
	PrivateKey string `yaml:"private_key" json:"private_key"`
	Subnet     string `yaml:"subnet,omitempty" json:"subnet,omitempty"` // DefaultWGSubnet by default
	MTU        int    `yaml:"mtu,omitempty" json:"mtu,omitempty"`       // 1408 by default
}

// User is a VPN user.
type User struct {
	Name string  `yaml:"name" json:"name"`
	UUID string  `yaml:"uuid" json:"uuid"`
	WG   *WGPeer `yaml:"wg,omitempty" json:"wg,omitempty"`
}

// WGPeer holds a user's WireGuard keys and address.
type WGPeer struct {
	PrivateKey   string `yaml:"private_key" json:"private_key"`
	PublicKey    string `yaml:"public_key" json:"public_key"`
	PresharedKey string `yaml:"preshared_key" json:"preshared_key"`
	Address      string `yaml:"address" json:"address"` // 10.70.0.N/32
}

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// ValidUserName reports whether a user name is acceptable: it ends up in outbound tags
// and in sing-box statistics counter names.
func ValidUserName(name string) bool { return nameRe.MatchString(name) }

// EffectiveTag is the inbound tag in the sing-box config: the given one or "<type>-<port>".
func (in Inbound) EffectiveTag() string {
	if in.Tag != "" {
		return in.Tag
	}
	return fmt.Sprintf("%s-%d", in.Type, in.Port)
}

// network is the transport the inbound listens on; ports conflict only within one transport.
func (in Inbound) network() string {
	if in.Type == TypeVLESSReality {
		return "tcp"
	}
	return "udp"
}

// WGSubnet returns the WireGuard subnet, taking the default into account.
func (p *WireGuardParams) WGSubnet() string {
	if p.Subnet == "" {
		return DefaultWGSubnet
	}
	return p.Subnet
}

// TLSMode returns the TLS mode, taking the default into account.
func (p *Hysteria2Params) TLSMode() TLSMode {
	if p.TLS == "" {
		return TLSSelfSigned
	}
	return p.TLS
}

// WireGuardInbound returns the node WireGuard inbound or nil.
func (n *Node) WireGuardInbound() *Inbound {
	for i := range n.Inbounds {
		if n.Inbounds[i].Type == TypeWireGuard {
			return &n.Inbounds[i]
		}
	}
	return nil
}

// Validate checks that a config can be built from the node and users:
// all secrets are set and tags, ports, names and addresses do not overlap.
func Validate(n *Node, users []User) error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if n.Host == "" {
		add("node: host is required")
	}
	if len(n.Inbounds) == 0 {
		add("node: at least one inbound is required")
	}

	tags := map[string]bool{}
	ports := map[string]bool{}
	wgCount := 0
	for i, in := range n.Inbounds {
		p := fmt.Sprintf("inbound[%d] %s", i, in.EffectiveTag())
		tag := in.EffectiveTag()
		if tags[tag] {
			add("%s: duplicate tag", p)
		}
		tags[tag] = true
		if in.Port == 0 {
			add("%s: port is required", p)
		}
		key := fmt.Sprintf("%s/%d", in.network(), in.Port)
		if ports[key] {
			add("%s: port %s is already used", p, key)
		}
		ports[key] = true

		switch in.Type {
		case TypeVLESSReality:
			if in.Reality == nil {
				add("%s: reality params are required", p)
				continue
			}
			if in.Reality.PrivateKey == "" {
				add("%s: reality private_key is empty", p)
			} else if _, err := RealityPublicKey(in.Reality.PrivateKey); err != nil {
				add("%s: reality private_key: %w", p, err)
			}
			if len(in.Reality.ShortIDs) == 0 {
				add("%s: reality short_ids are empty", p)
			}
			for _, id := range in.Reality.ShortIDs {
				if !isShortID(id) {
					add("%s: reality short_id %q must be 0-16 hex chars of even length", p, id)
				}
			}
			if in.Reality.ServerName == "" {
				add("%s: reality server_name is required", p)
			}
			if _, _, err := splitDest(in.Reality.Dest); err != nil {
				add("%s: reality dest: %w", p, err)
			}
		case TypeHysteria2:
			if in.Hysteria2 == nil {
				add("%s: hysteria2 params are required", p)
				continue
			}
			switch in.Hysteria2.TLSMode() {
			case TLSSelfSigned:
				if in.Hysteria2.CertPEM == "" || in.Hysteria2.KeyPEM == "" {
					add("%s: hysteria2 self-signed cert_pem and key_pem are required", p)
				}
			case TLSACME:
				if _, err := netip.ParseAddr(n.Host); err == nil || n.Host == "" {
					add("%s: hysteria2 acme needs a domain in node host, got %q", p, n.Host)
				}
			default:
				add("%s: hysteria2 unknown tls mode %q", p, in.Hysteria2.TLS)
			}
		case TypeWireGuard:
			wgCount++
			if in.WireGuard == nil {
				add("%s: wireguard params are required", p)
				continue
			}
			if _, err := parseWGKey(in.WireGuard.PrivateKey); err != nil {
				add("%s: wireguard private_key: %w", p, err)
			}
			if _, err := wgPrefix(in.WireGuard.WGSubnet()); err != nil {
				add("%s: wireguard subnet: %w", p, err)
			}
		default:
			add("%s: unknown type %q", p, in.Type)
		}
	}
	if wgCount > 1 {
		add("node: only one wireguard inbound is supported")
	}

	wg := n.WireGuardInbound()
	var subnet netip.Prefix
	if wg != nil && wg.WireGuard != nil {
		subnet, _ = wgPrefix(wg.WireGuard.WGSubnet())
	}
	names := map[string]bool{}
	uuids := map[string]bool{}
	addrs := map[string]bool{}
	for i, u := range users {
		p := fmt.Sprintf("user[%d] %s", i, u.Name)
		if !nameRe.MatchString(u.Name) {
			add("%s: name must match %s", p, nameRe)
		}
		if names[u.Name] {
			add("%s: duplicate name", p)
		}
		names[u.Name] = true
		if !uuidRe.MatchString(u.UUID) {
			add("%s: invalid uuid %q", p, u.UUID)
		}
		if uuids[strings.ToLower(u.UUID)] {
			add("%s: duplicate uuid", p)
		}
		uuids[strings.ToLower(u.UUID)] = true

		if wg == nil {
			continue
		}
		if u.WG == nil {
			add("%s: wg peer is required because node has wireguard", p)
			continue
		}
		pub, err := parseWGKey(u.WG.PublicKey)
		if err != nil {
			add("%s: wg public_key: %w", p, err)
		}
		if u.WG.PrivateKey != "" {
			if derived, err := WGPublicKey(u.WG.PrivateKey); err != nil {
				add("%s: wg private_key: %w", p, err)
			} else if pub != nil && derived != u.WG.PublicKey {
				add("%s: wg public_key does not match private_key", p)
			}
		}
		if _, err := parseWGKey(u.WG.PresharedKey); err != nil {
			add("%s: wg preshared_key: %w", p, err)
		}
		addr, err := netip.ParsePrefix(u.WG.Address)
		switch {
		case err != nil || addr.Bits() != 32:
			add("%s: wg address %q must be a /32", p, u.WG.Address)
		case subnet.IsValid() && !subnet.Contains(addr.Addr()):
			add("%s: wg address %s is outside %s", p, addr, subnet)
		case subnet.IsValid() && addr.Addr() == serverWGAddr(subnet):
			add("%s: wg address %s is reserved for the server", p, addr)
		case addrs[addr.String()]:
			add("%s: duplicate wg address %s", p, addr)
		}
		if err == nil {
			addrs[addr.String()] = true
		}
	}
	return errors.Join(errs...)
}

func isShortID(s string) bool {
	if len(s) > 16 || len(s)%2 != 0 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
