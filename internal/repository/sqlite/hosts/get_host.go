package hosts

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// GetHost returns an address by id.
func (r *Repository) GetHost(ctx context.Context, id int64) (*panel.Host, error) {
	return scanHost(r.db.QueryRowContext(ctx, `SELECT `+hostColumns+` FROM hosts WHERE id = ?`, id))
}
