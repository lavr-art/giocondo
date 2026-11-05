package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// ListNodes returns all nodes in ascending id order.
func (r *Repository) ListNodes(ctx context.Context) ([]panel.Node, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+nodeColumns+` FROM nodes ORDER BY id`)
	if err != nil {
		return nil, sqlite.MapErr(err)
	}
	defer func() { _ = rows.Close() }()

	var out []panel.Node

	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, *n)
	}

	return out, rows.Err()
}
