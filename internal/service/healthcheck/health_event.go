package healthcheck

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// HealthEvent re-evaluates the availability of node from its fresh probes on the active IP and emits
// node_down or node_up when it changed. Any protocol failing three checks in a row from any probe
// point makes the node down.
func (s *Service) HealthEvent(ctx context.Context, n panel.Node) error {
	s.nodeLock.Lock()
	defer s.nodeLock.Unlock()

	current, err := s.nodes.GetNode(ctx, n.ID)
	if err != nil {
		return err
	}

	n = *current
	if n.Status == panel.NodeNew || n.Status == panel.NodeDisabled {
		return nil
	}

	probes, err := s.probes.ListProbes(ctx, n.ID)
	if err != nil {
		return err
	}

	down, found := false, false

	for _, p := range probes {
		stale := p.CheckedAt.Before(s.hooks.Now().Add(-2 * max(s.Interval(), DefaultHealthInterval)))
		if (p.Point != "panel" && s.probePoints[p.Point] == nil) || p.IP != n.IP || stale {
			continue
		}

		found = true

		if p.Failures >= 3 {
			down = true
		}
	}

	if !found || down == n.DownNotified {
		return nil
	}

	if err = s.nodes.SetNodeDownNotified(ctx, n.ID, down); err != nil {
		return err
	}

	v, err := s.views.GetNode(ctx, n.ID)
	if err != nil {
		return err
	}

	typ := panel.EventNodeUp
	if down {
		typ = panel.EventNodeDown
	}

	s.hooks.Emit(panel.Event{Type: typ, Node: &v})

	return nil
}
