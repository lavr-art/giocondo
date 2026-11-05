package nodes

import (
	"context"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// UpdateNode changes the address, IP, region and enabled state.
func (s *Service) UpdateNode(
	ctx context.Context,
	id int64,
	in panel.UpdateNodeInput,
) (panel.NodeView, error) {
	s.nodeLock.Lock()
	defer s.nodeLock.Unlock()

	n, err := s.nodes.GetNode(ctx, id)
	if err != nil {
		return panel.NodeView{}, err
	}

	if err := s.network.Unlocked(ctx, id); err != nil {
		return panel.NodeView{}, err
	}

	if n.ManagedDNS && ((in.Host != nil && *in.Host != n.Host) || (in.IP != nil && *in.IP != n.IP)) {
		return panel.NodeView{}, operation.Invalid("use rotate-ip for managed node addresses")
	}

	if in.Host != nil {
		h := strings.TrimSpace(*in.Host)
		if h == "" {
			return panel.NodeView{}, operation.Invalid("host must not be empty")
		}

		n.Host = h
	}

	if in.IP != nil {
		n.IP = strings.TrimSpace(*in.IP)
	}

	if in.Region != nil {
		n.Region = strings.TrimSpace(*in.Region)
	}

	if in.Disabled != nil {
		switch {
		case *in.Disabled:
			n.Status = panel.NodeDisabled
		case n.Status == panel.NodeDisabled:
			n.Status = panel.NodeOffline // the agent reconnects and becomes online
		}
	}

	if err := s.nodes.UpdateNode(ctx, n); err != nil {
		return panel.NodeView{}, err
	}

	logger.FromCtx(ctx).Info("node updated", "node", n.Name, "status", n.Status)
	s.hooks.Kick()

	return s.views.GetNode(ctx, id)
}
