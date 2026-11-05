package nodeconfig

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// EnsureWG ensures WireGuard is configured.
func (s *Coordinator) EnsureWG(ctx context.Context) error {
	nodes, err := s.nodes.ListNodes(ctx)
	if err != nil {
		return err
	}

	for _, n := range nodes {
		ins, err := s.nodes.ListInbounds(ctx, n.ID)
		if err != nil {
			return err
		}

		for _, in := range ins {
			if in.Spec.Type == protocol.TypeWireGuard && in.Spec.WireGuard != nil {
				return s.allocator.EnsureWGPeers(ctx, s.nodes, s.users, in.Spec.WireGuard.WGSubnet())
			}
		}
	}

	return nil
}
