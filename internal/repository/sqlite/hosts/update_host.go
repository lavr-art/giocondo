package hosts

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// UpdateHost stores the address, port, SNI, path and order. Moving it to another inbound is not allowed.
func (r *Repository) UpdateHost(ctx context.Context, h *panel.Host) error {
	return sqlite.Affected(r.db.ExecContext(ctx,
		`UPDATE hosts SET address = ?, port = ?, sni = ?, path = ?, sort = ? WHERE id = ?`,
		h.Address, h.Port, h.SNI, h.Path, h.Sort, h.ID))
}
