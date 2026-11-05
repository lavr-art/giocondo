package network

import (
	"context"

	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// SetManagedDNS records whether DNS is managed.
func (r *Repository) SetManagedDNS(ctx context.Context, nodeID int64, host, recordID string) error {
	return sqlite.Affected(r.db.ExecContext(ctx,
		`UPDATE nodes SET host=?,dns_record_id=?,managed_dns=TRUE WHERE id=?`,
		host, recordID, nodeID))
}
