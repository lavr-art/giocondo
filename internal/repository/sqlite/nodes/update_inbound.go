package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// UpdateInbound stores the type, tag, port and parameters. Moving an inbound to another node is not allowed.
func (r *Repository) UpdateInbound(ctx context.Context, in *panel.Inbound) error {
	params, err := inboundParams(in.Spec)
	if err != nil {
		return err
	}

	return sqlite.Affected(r.db.ExecContext(ctx,
		`UPDATE inbounds SET type = ?, tag = ?, port = ?, params = ? WHERE id = ?`,
		in.Spec.Type, in.Spec.Tag, in.Spec.Port, params, in.ID))
}
