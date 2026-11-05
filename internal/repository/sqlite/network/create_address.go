package network

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// CreateAddress creates a node address.
func (r *Repository) CreateAddress(ctx context.Context, a *panel.Address) error {
	return sqlite.MapErr(r.db.QueryRowContext(ctx,
		`INSERT INTO node_addresses(node_id,installation_id,ip) VALUES(?,?,?) RETURNING id`,
		a.NodeID, a.InstallationID, a.IP).Scan(&a.ID))
}
