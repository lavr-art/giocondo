package admin

import (
	"context"
	"net"
	"regexp"
	"strings"

	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
)

// HostView is a host override: the address clients receive in the subscription instead of the node host.
type HostView struct {
	ID         int64               `json:"id"`
	InboundTag string              `json:"inbound"`
	Type       singbox.InboundType `json:"type"`
	Address    string              `json:"address"`
	Port       uint16              `json:"port"` // 0 means the inbound port
	SNI        string              `json:"sni"`
	Path       string              `json:"path"`
	Sort       int                 `json:"sort"`
}

// HostInput is a new override or its full replacement (the inbound does not change on update).
type HostInput struct {
	Inbound string `json:"inbound"` // node inbound tag, e.g. vless
	Address string `json:"address"`
	Port    uint16 `json:"port"`
	SNI     string `json:"sni"`
	Path    string `json:"path"`
	Sort    int    `json:"sort"`
}

var domainRe = regexp.MustCompile(`^(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)(?:\.(?i:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?))*$`)

func validHostname(s string) bool {
	return net.ParseIP(s) != nil || (len(s) <= 253 && domainRe.MatchString(s))
}

func (s *Service) checkHost(in store.Inbound, h *HostInput) error {
	h.Address, h.SNI, h.Path = strings.TrimSpace(h.Address), strings.TrimSpace(h.SNI), strings.TrimSpace(h.Path)
	if !validHostname(h.Address) {
		return invalid("address %q: domain or IP", h.Address)
	}
	if h.SNI != "" && !validHostname(h.SNI) {
		return invalid("sni %q: domain", h.SNI)
	}
	switch in.Spec.Type {
	case singbox.TypeVLESSReality:
		// A Reality server accepts only its own server_name: another SNI breaks the connection.
		if h.SNI != "" && in.Spec.Reality != nil && h.SNI != in.Spec.Reality.ServerName {
			return invalid("Reality accepts only SNI %q set on the node", in.Spec.Reality.ServerName)
		}
	case singbox.TypeWireGuard:
		if h.SNI != "" {
			return invalid("WireGuard has no SNI")
		}
	}
	return nil
}

func hostView(h store.Host, in store.Inbound) HostView {
	return HostView{ID: h.ID, InboundTag: in.Spec.Tag, Type: in.Spec.Type, Address: h.Address, Port: h.Port,
		SNI: h.SNI, Path: h.Path, Sort: h.Sort}
}

// ListHosts returns the node overrides ordered by inbound, sort.
func (s *Service) ListHosts(ctx context.Context, nodeID int64) ([]HostView, error) {
	if _, err := s.Store.GetNode(ctx, nodeID); err != nil {
		return nil, err
	}
	ins, err := s.Store.ListInbounds(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	byID := map[int64]store.Inbound{}
	for _, in := range ins {
		byID[in.ID] = in
	}
	hosts, err := s.Store.ListHosts(ctx)
	if err != nil {
		return nil, err
	}
	out := []HostView{}
	for _, h := range hosts {
		if in, ok := byID[h.InboundID]; ok {
			out = append(out, hostView(h, in))
		}
	}
	return out, nil
}

// CreateHost adds an override to a node inbound (by tag).
func (s *Service) CreateHost(ctx context.Context, nodeID int64, in HostInput) (HostView, error) {
	ins, err := s.Store.ListInbounds(ctx, nodeID)
	if err != nil {
		return HostView{}, err
	}
	if _, err := s.Store.GetNode(ctx, nodeID); err != nil {
		return HostView{}, err
	}
	var ib *store.Inbound
	for i := range ins {
		if ins[i].Spec.Tag == strings.TrimSpace(in.Inbound) {
			ib = &ins[i]
		}
	}
	if ib == nil {
		return HostView{}, invalid("node has no inbound %q", in.Inbound)
	}
	n, e := s.Store.GetNode(ctx, nodeID)
	if e != nil {
		return HostView{}, e
	}
	if n.ManagedDNS && net.ParseIP(in.Address) != nil {
		return HostView{}, invalid("managed nodes require domain host overrides")
	}
	if err := s.checkHost(*ib, &in); err != nil {
		return HostView{}, err
	}
	h := store.Host{InboundID: ib.ID, Address: in.Address, Port: in.Port, SNI: in.SNI, Path: in.Path, Sort: in.Sort}
	if err := s.Store.CreateHost(ctx, &h); err != nil {
		return HostView{}, err
	}
	s.log().Info("host added", "inbound", ib.Spec.Tag, "address", h.Address)
	return hostView(h, *ib), nil
}

func (s *Service) hostWithInbound(ctx context.Context, id int64) (*store.Host, store.Inbound, int64, error) {
	h, err := s.Store.GetHost(ctx, id)
	if err != nil {
		return nil, store.Inbound{}, 0, err
	}
	nodes, err := s.Store.ListNodes(ctx)
	if err != nil {
		return nil, store.Inbound{}, 0, err
	}
	for _, n := range nodes {
		ins, err := s.Store.ListInbounds(ctx, n.ID)
		if err != nil {
			return nil, store.Inbound{}, 0, err
		}
		for _, in := range ins {
			if in.ID == h.InboundID {
				return h, in, n.ID, nil
			}
		}
	}
	return nil, store.Inbound{}, 0, store.ErrNotFound
}

// UpdateHost replaces the address, port, SNI, path and order of an override.
func (s *Service) UpdateHost(ctx context.Context, id int64, in HostInput) (HostView, error) {
	h, ib, nodeID, err := s.hostWithInbound(ctx, id)
	if err != nil {
		return HostView{}, err
	}
	n, e := s.Store.GetNode(ctx, nodeID)
	if e != nil {
		return HostView{}, e
	}
	if n.ManagedDNS && net.ParseIP(in.Address) != nil {
		return HostView{}, invalid("managed nodes require domain host overrides")
	}
	if err := s.checkHost(ib, &in); err != nil {
		return HostView{}, err
	}
	h.Address, h.Port, h.SNI, h.Path, h.Sort = in.Address, in.Port, in.SNI, in.Path, in.Sort
	if err := s.Store.UpdateHost(ctx, h); err != nil {
		return HostView{}, err
	}
	return hostView(*h, ib), nil
}

// DeleteHost deletes an override and returns the id of the node it belonged to.
func (s *Service) DeleteHost(ctx context.Context, id int64) (int64, error) {
	_, _, nodeID, err := s.hostWithInbound(ctx, id)
	if err != nil {
		return 0, err
	}
	return nodeID, s.Store.DeleteHost(ctx, id)
}
