package network

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// CreateInstallation records a new installation.
func (r *Repository) CreateInstallation(ctx context.Context, i *panel.Installation) error {
	if i.State == "" {
		i.State = panel.InstallationCandidate
	}

	return sqlite.MapErr(r.db.QueryRowContext(ctx,
		`INSERT INTO node_installations(node_id,identity,ip,state) VALUES(?,?,?,?) RETURNING id`,
		i.NodeID, i.Identity, i.IP, i.State).Scan(&i.ID))
}
