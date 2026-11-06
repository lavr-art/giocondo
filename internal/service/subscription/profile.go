package subscription

import (
	"errors"
	"fmt"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/singbox"
)

// Protocol names are the same in share-link schemes, sing-box outbound types and Mihomo proxy types.
const (
	protoVLESS     = "vless"
	protoHysteria2 = "hysteria2"
	protoWireGuard = "wireguard"
)

// Profile is everything needed for one user's subscription.
type Profile struct {
	User     panel.User
	Usage    panel.Usage
	Nodes    []NodeProfile
	Settings panel.SubscriptionSettings
}

// NodeProfile is a node and its protocols.
type NodeProfile struct {
	Node     panel.Node
	Inbounds []protocol.Inbound
	// Hosts are host overrides by inbound tag. If an inbound has them, the subscription uses
	// them (one server per address) instead of the node host.
	Hosts map[string][]panel.Host
}

// target is where the client connects.
type target struct {
	Address string
	Port    uint16
	SNI     string // empty means the protocol default
}

func targets(np NodeProfile, in protocol.Inbound) []target {
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
	Type protocol.InboundType
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

var typeLabels = map[protocol.InboundType]string{
	protocol.TypeVLESSReality: "Reality",
	protocol.TypeHysteria2:    "Hysteria2",
	protocol.TypeWireGuard:    "WireGuard",
}

// servers expands the profile into servers in the order of nodes and their protocols.
// WireGuard is skipped if the user has no WG keys.
func servers(p Profile) ([]server, error) {
	var out []server

	used := map[string]bool{}

	for _, np := range p.Nodes {
		for _, in := range np.Inbounds {
			s, ok, err := serverFor(np, in, p.User)
			if err != nil {
				return nil, fmt.Errorf("node %s inbound %s: %w", np.Node.Name, in.EffectiveTag(), err)
			}

			if ok {
				out = append(out, expandTargets(np, in, s, used)...)
			}
		}
	}

	return out, nil
}

// serverFor computes the client parameters of one inbound; ok is false for an inbound the user cannot use.
func serverFor(np NodeProfile, in protocol.Inbound, user panel.User) (server, bool, error) {
	s := server{Type: in.Type}

	switch in.Type {
	case protocol.TypeVLESSReality:
		if in.Reality == nil {
			return s, false, errors.New("no reality params")
		}

		pub, err := protocol.RealityPublicKey(in.Reality.PrivateKey)
		if err != nil {
			return s, false, err
		}

		s.UUID, s.PublicKey, s.SNI = user.UUID, pub, in.Reality.ServerName
		if len(in.Reality.ShortIDs) > 0 {
			s.ShortID = in.Reality.ShortIDs[0]
		}
	case protocol.TypeHysteria2:
		if in.Hysteria2 == nil {
			return s, false, errors.New("no hysteria2 params")
		}

		s.UUID, s.SNI, s.ObfsPassword = user.UUID, np.Node.Host, in.Hysteria2.ObfsPassword
		if in.Hysteria2.TLSMode() == protocol.TLSSelfSigned {
			pin, err := singbox.CertSHA256(in.Hysteria2.CertPEM)
			if err != nil {
				return s, false, err
			}

			s.Insecure, s.PinSHA256 = true, pin
		}
	case protocol.TypeWireGuard:
		if user.WG == nil || in.WireGuard == nil {
			return s, false, nil
		}

		pub, err := protocol.WGPublicKey(in.WireGuard.PrivateKey)
		if err != nil {
			return s, false, err
		}

		s.WGPrivateKey, s.WGPeerPubKey = user.WG.PrivateKey, pub
		s.WGPresharedKey, s.WGAddress = user.WG.PresharedKey, user.WG.Address

		s.MTU = in.WireGuard.MTU
		if s.MTU == 0 {
			s.MTU = singbox.DefaultWGMTU
		}
	default:
		return s, false, nil
	}

	return s, true, nil
}

// expandTargets repeats the server for every host override; used keeps names unique across the profile.
func expandTargets(np NodeProfile, in protocol.Inbound, s server, used map[string]bool) []server {
	ts := targets(np, in)
	out := make([]server, 0, len(ts))

	for _, t := range ts {
		srv := s
		srv.Host, srv.Port = t.Address, t.Port
		// The Reality SNI must equal the node server_name (admin checks this), so
		// an SNI override only makes sense for Hysteria2, e.g. an IP address with a certificate for a domain.
		if t.SNI != "" && in.Type == protocol.TypeHysteria2 {
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

	return out
}

// Status is why the user cannot be given a subscription; an empty string means they can.
func (p Profile) Status(now time.Time) string {
	switch p.User.Status(p.Usage, now) {
	case panel.UserActive:
		return ""
	case panel.UserDisabled:
		return "disabled"
	case panel.UserExpired:
		return "expired"
	case panel.UserLimited:
		return "traffic limit reached"
	default:
		return ""
	}
}

// UserInfo is the subscription-userinfo header value. total=0 and expire=0 mean unlimited.
func (p Profile) UserInfo() string {
	var expire int64
	if p.User.ExpiresAt != nil {
		expire = p.User.ExpiresAt.Unix()
	}

	return fmt.Sprintf(
		"upload=%d; download=%d; total=%d; expire=%d",
		p.Usage.Up,
		p.Usage.Down,
		p.User.TrafficLimitBytes,
		expire,
	)
}
