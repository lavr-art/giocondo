package network

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// InstallationByIdentity returns an installation by agent identity.
func (r *Repository) InstallationByIdentity(ctx context.Context, identity string) (*panel.Installation, error) {
	var i panel.Installation

	err := r.db.QueryRowContext(ctx,
		`SELECT id,node_id,identity,ip,state,last_seen,applied_hash,apply_error FROM node_installations WHERE identity=?`,
		identity).Scan(
		&i.ID, &i.NodeID, &i.Identity, &i.IP, &i.State, sqlite.NullTime(&i.LastSeen), &i.AppliedHash, &i.ApplyError)

	return &i, sqlite.MapErr(err)
}
