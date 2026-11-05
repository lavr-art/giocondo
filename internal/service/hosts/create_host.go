package hosts

import (
	"context"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// CreateHost adds an override to a node inbound (by tag).
func (s *Service) CreateHost(ctx context.Context, nodeID int64, in panel.HostInput) (panel.HostView, error) {
	node, err := s.nodes.GetNode(ctx, nodeID)
	if err != nil {
		return panel.HostView{}, err
	}

	ins, err := s.nodes.ListInbounds(ctx, nodeID)
	if err != nil {
		return panel.HostView{}, err
	}

	tag := strings.TrimSpace(in.Inbound)

	var ib *panel.Inbound

	for i := range ins {
		if ins[i].Spec.Tag == tag {
			ib = &ins[i]

			break
		}
	}

	if ib == nil {
		return panel.HostView{}, operation.Invalid("node has no inbound %q", in.Inbound)
	}

	if err := s.checkHost(*node, *ib, &in); err != nil {
		return panel.HostView{}, err
	}

	h := panel.Host{InboundID: ib.ID, Address: in.Address, Port: in.Port, SNI: in.SNI, Path: in.Path, Sort: in.Sort}
	if err := s.hosts.CreateHost(ctx, &h); err != nil {
		return panel.HostView{}, err
	}

	logger.FromCtx(ctx).Info("host added", "inbound", ib.Spec.Tag, "address", h.Address)

	return hostView(h, *ib), nil
}
