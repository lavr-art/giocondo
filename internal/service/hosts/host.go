package hosts

import (
	"context"
	"net"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// checkHost normalizes h and checks it against the node and the inbound it overrides.
func (s *Service) checkHost(node panel.Node, in panel.Inbound, h *panel.HostInput) error {
	h.Address, h.SNI, h.Path = strings.TrimSpace(h.Address), strings.TrimSpace(h.SNI), strings.TrimSpace(h.Path)
	if !panel.ValidHostname(h.Address) {
		return operation.Invalid("address %q: domain or IP", h.Address)
	}

	// The checks run on the trimmed address, so surrounding spaces cannot pass an IP as a domain.
	if node.ManagedDNS && net.ParseIP(h.Address) != nil {
		return operation.Invalid("managed nodes require domain host overrides")
	}

	if h.SNI != "" && !panel.ValidHostname(h.SNI) {
		return operation.Invalid("sni %q: domain", h.SNI)
	}

	switch in.Spec.Type {
	case protocol.TypeVLESSReality:
		// A Reality server accepts only its own server_name: another SNI breaks the connection.
		if h.SNI != "" && in.Spec.Reality != nil && h.SNI != in.Spec.Reality.ServerName {
			return operation.Invalid("Reality accepts only SNI %q set on the node", in.Spec.Reality.ServerName)
		}
	case protocol.TypeHysteria2:
		// Hysteria2 uses the host address; any SNI override is allowed.
	case protocol.TypeWireGuard:
		if h.SNI != "" {
			return operation.Invalid("WireGuard has no SNI")
		}
	}

	return nil
}

func hostView(h panel.Host, in panel.Inbound) panel.HostView {
	return panel.HostView{ID: h.ID, InboundTag: in.Spec.Tag, Type: in.Spec.Type, Address: h.Address, Port: h.Port,
		SNI: h.SNI, Path: h.Path, Sort: h.Sort}
}

func (s *Service) hostWithInbound(ctx context.Context, id int64) (*panel.Host, panel.Inbound, int64, error) {
	h, err := s.hosts.GetHost(ctx, id)
	if err != nil {
		return nil, panel.Inbound{}, 0, err
	}

	nodes, err := s.nodes.ListNodes(ctx)
	if err != nil {
		return nil, panel.Inbound{}, 0, err
	}

	for _, n := range nodes {
		ins, err := s.nodes.ListInbounds(ctx, n.ID)
		if err != nil {
			return nil, panel.Inbound{}, 0, err
		}

		for _, in := range ins {
			if in.ID == h.InboundID {
				return h, in, n.ID, nil
			}
		}
	}

	return nil, panel.Inbound{}, 0, panel.ErrNotFound
}
