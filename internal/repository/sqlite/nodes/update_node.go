package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// UpdateNode stores name, host, ip, region and status.
func (r *Repository) UpdateNode(ctx context.Context, n *panel.Node) error {
	return sqlite.Affected(r.db.ExecContext(ctx,
		`UPDATE nodes SET name = ?, host = ?, ip = ?, region = ?, status = ? WHERE id = ?`,
		n.Name, n.Host, n.IP, n.Region, n.Status, n.ID))
}
