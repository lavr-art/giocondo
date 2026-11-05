package nodeprobe

import (
	"context"
	"fmt"
	"maps"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// CheckAddress validates an address.
func (s *Service) CheckAddress(ctx context.Context, n panel.Node, ip string) error {
	u, err := s.monitor.EnsureMonitor(ctx, n.ID)
	if err != nil {
		return err
	}

	ins, err := s.nodes.ListInbounds(ctx, n.ID)
	if err != nil {
		return err
	}

	if len(ins) == 0 {
		return operation.Invalid("node has no protocols")
	}

	points := map[string]probe.Checker{"panel": s.checker}
	maps.Copy(points, s.probePoints)

	for _, in := range ins {
		for point, c := range points {
			if err := s.CheckProtocol(
				ctx,
				c,
				probe.Request{IP: ip, Host: n.Host, Inbound: in.Spec, User: *u},
			); err != nil {
				return fmt.Errorf("%s %s readiness: %w", point, in.Spec.EffectiveTag(), err)
			}
		}
	}

	return nil
}
