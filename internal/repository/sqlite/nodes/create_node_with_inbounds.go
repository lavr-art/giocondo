package nodes

import (
	"context"
	"database/sql"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// CreateNodeWithInbounds commits a complete node aggregate. Caller-visible IDs are
// published only after commit so a failed operation can be retried safely.
func (r *Repository) CreateNodeWithInbounds(ctx context.Context, n *panel.Node, inbounds []panel.Inbound) error {
	node := *n

	ins := append([]panel.Inbound(nil), inbounds...)

	err := r.db.InTx(ctx, func(tx *sql.Tx) error {
		if err := createNode(ctx, tx, &node); err != nil {
			return err
		}

		for i := range ins {
			ins[i].NodeID = node.ID
			if err := createInbound(ctx, tx, &ins[i]); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return sqlite.MapErr(err)
	}

	*n = node

	copy(inbounds, ins)

	return nil
}
