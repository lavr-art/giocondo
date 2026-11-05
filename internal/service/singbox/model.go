package singbox

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/protocol"
)

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// ValidUserName reports whether a user name is acceptable: it ends up in outbound tags
// and in sing-box statistics counter names.
func ValidUserName(name string) bool { return nameRe.MatchString(name) }

// problems collects validation errors in the order they are found.
type problems func(format string, args ...any)

// Validate checks that a config can be built from the node and users:
// all secrets are set and tags, ports, names and addresses do not overlap.
func Validate(n *protocol.Node, users []protocol.User) error {
	var errs []error

	add := problems(func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) })

	if n.Host == "" {
		add("node: host is required")
	}

	if len(n.Inbounds) == 0 {
		add("node: at least one inbound is required")
	}

	validateInbounds(n, add)
	validateUsers(n, users, add)

	return errors.Join(errs...)
}

func validateInbounds(n *protocol.Node, add problems) {
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

		key := fmt.Sprintf("%s/%d", inboundNetwork(in), in.Port)
		if ports[key] {
			add("%s: port %s is already used", p, key)
		}

		ports[key] = true

		switch in.Type {
		case protocol.TypeVLESSReality:
			validateReality(p, in.Reality, add)
		case protocol.TypeHysteria2:
			validateHysteria2(p, n.Host, in.Hysteria2, add)
		case protocol.TypeWireGuard:
			wgCount++

			validateWireGuard(p, in.WireGuard, add)
		default:
			add("%s: unknown type %q", p, in.Type)
		}
	}

	if wgCount > 1 {
		add("node: only one wireguard inbound is supported")
	}
}

func validateReality(p string, r *protocol.RealityParams, add problems) {
	if r == nil {
		add("%s: reality params are required", p)

		return
	}

	if r.PrivateKey == "" {
		add("%s: reality private_key is empty", p)
	} else if _, err := protocol.RealityPublicKey(r.PrivateKey); err != nil {
		add("%s: reality private_key: %w", p, err)
	}

	if len(r.ShortIDs) == 0 {
		add("%s: reality short_ids are empty", p)
	}

	for _, id := range r.ShortIDs {
		if !isShortID(id) {
			add("%s: reality short_id %q must be 0-16 hex chars of even length", p, id)
		}
	}

	if r.ServerName == "" {
		add("%s: reality server_name is required", p)
	}

	if _, _, err := splitDest(r.Dest); err != nil {
		add("%s: reality dest: %w", p, err)
	}
}

func validateHysteria2(p, host string, h *protocol.Hysteria2Params, add problems) {
	if h == nil {
		add("%s: hysteria2 params are required", p)

		return
	}

	switch h.TLSMode() {
	case protocol.TLSSelfSigned:
		if h.CertPEM == "" || h.KeyPEM == "" {
			add("%s: hysteria2 self-signed cert_pem and key_pem are required", p)
		}
	case protocol.TLSACME:
		if _, err := netip.ParseAddr(host); err == nil || host == "" {
			add("%s: hysteria2 acme needs a domain in node host, got %q", p, host)
		}
	default:
		add("%s: hysteria2 unknown tls mode %q", p, h.TLS)
	}
}

func validateWireGuard(p string, w *protocol.WireGuardParams, add problems) {
	if w == nil {
		add("%s: wireguard params are required", p)

		return
	}

	if _, err := protocol.ParseWGKey(w.PrivateKey); err != nil {
		add("%s: wireguard private_key: %w", p, err)
	}

	if _, err := wgPrefix(w.WGSubnet()); err != nil {
		add("%s: wireguard subnet: %w", p, err)
	}
}

func validateUsers(n *protocol.Node, users []protocol.User, add problems) {
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

		validateUserWG(p, u.WG, subnet, addrs, add)
	}
}

// validateUserWG checks a user's WireGuard peer; addrs collects the addresses already taken.
func validateUserWG(p string, peer *protocol.WGPeer, subnet netip.Prefix, addrs map[string]bool, add problems) {
	pub, err := protocol.ParseWGKey(peer.PublicKey)
	if err != nil {
		add("%s: wg public_key: %w", p, err)
	}

	if peer.PrivateKey != "" {
		if derived, err := protocol.WGPublicKey(peer.PrivateKey); err != nil {
			add("%s: wg private_key: %w", p, err)
		} else if pub != nil && derived != peer.PublicKey {
			add("%s: wg public_key does not match private_key", p)
		}
	}

	if _, err := protocol.ParseWGKey(peer.PresharedKey); err != nil {
		add("%s: wg preshared_key: %w", p, err)
	}

	addr, err := netip.ParsePrefix(peer.Address)
	switch {
	case err != nil || addr.Bits() != 32:
		add("%s: wg address %q must be a /32", p, peer.Address)
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

func inboundNetwork(in protocol.Inbound) string {
	if in.Type == protocol.TypeVLESSReality {
		return "tcp"
	}

	return "udp"
}
