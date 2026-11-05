package nodes

import (
	"context"
	"fmt"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/nodeconfig"
	"github.com/artemlavr/giocondo/internal/service/singbox"
)

// ExportWireGuard exports WireGuard state from a node.
func (s *Reader) ExportWireGuard(ctx context.Context, userID, nodeID int64) ([]byte, string, error) {
	u, err := s.users.GetUser(ctx, userID)
	if err != nil {
		return nil, "", err
	}

	n, err := s.nodes.GetNode(ctx, nodeID)
	if err != nil {
		return nil, "", err
	}

	inbounds, err := s.nodes.ListInbounds(ctx, nodeID)
	if err != nil {
		return nil, "", err
	}

	spec := nodeconfig.Node(*n, inbounds)

	data, err := singbox.WireGuardClientConfig(&spec, nodeconfig.User(*u))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", panel.ErrInvalid, err)
	}

	return []byte(data), fmt.Sprintf("%s-%s.conf", u.Name, n.Name), nil
}
