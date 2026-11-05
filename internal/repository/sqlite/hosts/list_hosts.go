package hosts

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// ListHosts returns all addresses ordered by inbound, sort, id.
func (r *Repository) ListHosts(ctx context.Context) ([]panel.Host, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+hostColumns+` FROM hosts ORDER BY inbound_id, sort, id`)
	if err != nil {
		return nil, sqlite.MapErr(err)
	}
	defer func() { _ = rows.Close() }()

	var out []panel.Host

	for rows.Next() {
		h, err := scanHost(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, *h)
	}

	return out, rows.Err()
}
