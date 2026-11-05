package hosts

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// ListHosts returns the node overrides ordered by inbound, sort.
func (s *Service) ListHosts(ctx context.Context, nodeID int64) ([]panel.HostView, error) {
	if _, err := s.nodes.GetNode(ctx, nodeID); err != nil {
		return nil, err
	}

	ins, err := s.nodes.ListInbounds(ctx, nodeID)
	if err != nil {
		return nil, err
	}

	byID := map[int64]panel.Inbound{}
	for _, in := range ins {
		byID[in.ID] = in
	}

	hosts, err := s.hosts.ListHosts(ctx)
	if err != nil {
		return nil, err
	}

	out := []panel.HostView{}

	for _, h := range hosts {
		if in, ok := byID[h.InboundID]; ok {
			out = append(out, hostView(h, in))
		}
	}

	return out, nil
}
