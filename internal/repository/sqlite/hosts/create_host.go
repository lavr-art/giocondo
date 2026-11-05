package hosts

import (
	"context"
	"fmt"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// CreateHost adds an inbound address.
func (r *Repository) CreateHost(ctx context.Context, h *panel.Host) error {
	if h.Address == "" {
		return fmt.Errorf("host address is required")
	}

	err := r.db.QueryRowContext(ctx,
		`INSERT INTO hosts (inbound_id, address, port, sni, path, sort)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id`,
		h.InboundID, h.Address, h.Port, h.SNI, h.Path, h.Sort).Scan(&h.ID)

	return sqlite.MapErr(err)
}
