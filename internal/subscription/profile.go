package subscription

import (
	"fmt"
	"time"

	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
)

// Profile is everything needed for one user's subscription.
type Profile struct {
	User  store.User
	Usage store.Usage
	Nodes []NodeProfile
}

// NodeProfile is a node and its protocols.
type NodeProfile struct {
	Node     store.Node
	Inbounds []singbox.Inbound
	// Hosts are host overrides by inbound tag. If an inbound has them, the subscription uses
	// them (one server per address) instead of the node host.
	Hosts map[string][]store.Host
}

// target is where the client connects.
type target struct {
	Address string
	Port    uint16
	SNI     string // empty means the protocol default
}

func targets(np NodeProfile, in singbox.Inbound) []target {
	hosts := np.Hosts[in.EffectiveTag()]
	if len(hosts) == 0 {
		return []target{{Address: np.Node.Host, Port: in.Port}}
	}
	out := make([]target, 0, len(hosts))
	for _, h := range hosts {
		t := target{Address: h.Address, Port: h.Port, SNI: h.SNI}
		if t.Port == 0 {
			t.Port = in.Port
		}
		out = append(out, t)
	}
	return out
}

// server is one server in the client with the client parameters already computed.
// The formats (links, sing-box JSON, Clash YAML) differ only in how it is rendered.
type server struct {
	Name string
	Type singbox.InboundType
	Host string
	Port uint16

	// VLESS Reality
	UUID      string
	PublicKey string
	ShortID   string
	SNI       string

	// Hysteria2 (the password is the user UUID; SNI is shared with Reality)
	Insecure     bool   // self-signed certificate
	PinSHA256    string // hex SHA-256 of the certificate, self-signed only
	ObfsPassword string

	// WireGuard
	WGPrivateKey   string
	WGPeerPubKey   string
	WGPresharedKey string
	WGAddress      string
	MTU            int
}

var typeLabels = map[singbox.InboundType]string{
	singbox.TypeVLESSReality: "Reality",
	singbox.TypeHysteria2:    "Hysteria2",
	singbox.TypeWireGuard:    "WireGuard",
}

// servers expands the profile into servers in the order of nodes and their protocols.
// WireGuard is skipped if the user has no WG keys.
func servers(p Profile) ([]server, error) {
	var out []server
	used := map[string]bool{}
	for _, np := range p.Nodes {
		for _, in := range np.Inbounds {
			s := server{Type: in.Type}
			switch in.Type {
			case singbox.TypeVLESSReality:
				if in.Reality == nil {
					return nil, fmt.Errorf("node %s inbound %s: no reality params", np.Node.Name, in.EffectiveTag())
				}
				pub, err := singbox.RealityPublicKey(in.Reality.PrivateKey)
				if err != nil {
					return nil, fmt.Errorf("node %s inbound %s: %w", np.Node.Name, in.EffectiveTag(), err)
				}
				s.UUID, s.PublicKey, s.SNI = p.User.UUID, pub, in.Reality.ServerName
				if len(in.Reality.ShortIDs) > 0 {
					s.ShortID = in.Reality.ShortIDs[0]
				}
			case singbox.TypeHysteria2:
				if in.Hysteria2 == nil {
					return nil, fmt.Errorf("node %s inbound %s: no hysteria2 params", np.Node.Name, in.EffectiveTag())
				}
				s.UUID, s.SNI, s.ObfsPassword = p.User.UUID, np.Node.Host, in.Hysteria2.ObfsPassword
				if in.Hysteria2.TLSMode() == singbox.TLSSelfSigned {
					pin, err := singbox.CertSHA256(in.Hysteria2.CertPEM)
					if err != nil {
						return nil, fmt.Errorf("node %s inbound %s: %w", np.Node.Name, in.EffectiveTag(), err)
					}
					s.Insecure, s.PinSHA256 = true, pin
				}
			case singbox.TypeWireGuard:
				if p.User.WG == nil || in.WireGuard == nil {
					continue
				}
				pub, err := singbox.WGPublicKey(in.WireGuard.PrivateKey)
				if err != nil {
					return nil, fmt.Errorf("node %s inbound %s: %w", np.Node.Name, in.EffectiveTag(), err)
				}
				s.WGPrivateKey, s.WGPeerPubKey = p.User.WG.PrivateKey, pub
				s.WGPresharedKey, s.WGAddress = p.User.WG.PresharedKey, p.User.WG.Address
				s.MTU = in.WireGuard.MTU
				if s.MTU == 0 {
					s.MTU = singbox.DefaultWGMTU
				}
			default:
				continue
			}
			ts := targets(np, in)
			for _, t := range ts {
				srv := s
				srv.Host, srv.Port = t.Address, t.Port
				// The Reality SNI must equal the node server_name (admin checks this), so
				// an SNI override only makes sense for Hysteria2, e.g. an IP address with a certificate for a domain.
				if t.SNI != "" && in.Type == singbox.TypeHysteria2 {
					srv.SNI = t.SNI
				}
				srv.Name = np.Node.Name + " · " + typeLabels[in.Type]
				if len(ts) > 1 {
					srv.Name += " · " + t.Address
				}
				if used[srv.Name] {
					srv.Name += " (" + in.EffectiveTag() + ")"
				}
				used[srv.Name] = true
				out = append(out, srv)
			}
		}
	}
	return out, nil
}

// Status is why the user cannot be given a subscription; an empty string means they can.
func (p Profile) Status(now time.Time) string {
	u := p.User
	switch {
	case !u.Enabled:
		return "disabled"
	case u.ExpiresAt != nil && !u.ExpiresAt.After(now):
		return "expired"
	case u.TrafficLimitBytes > 0 && p.Usage.Total() >= u.TrafficLimitBytes:
		return "traffic limit reached"
	}
	return ""
}

// UserInfo is the subscription-userinfo header value. total=0 and expire=0 mean unlimited.
func (p Profile) UserInfo() string {
	var expire int64
	if p.User.ExpiresAt != nil {
		expire = p.User.ExpiresAt.Unix()
	}
	return fmt.Sprintf("upload=%d; download=%d; total=%d; expire=%d", p.Usage.Up, p.Usage.Down, p.User.TrafficLimitBytes, expire)
}
