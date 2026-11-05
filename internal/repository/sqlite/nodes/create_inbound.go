package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// CreateInbound adds a protocol to a node. An empty tag is replaced with protocol.Inbound.EffectiveTag.
func (r *Repository) CreateInbound(ctx context.Context, in *panel.Inbound) error {
	return createInbound(ctx, r.db, in)
}
