package tokens

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// ListTokens returns all tokens in ascending id order.
func (r *Repository) ListTokens(ctx context.Context) ([]panel.APIToken, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+tokenColumns+` FROM api_tokens ORDER BY id`)
	if err != nil {
		return nil, sqlite.MapErr(err)
	}
	defer func() { _ = rows.Close() }()

	var out []panel.APIToken

	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, *t)
	}

	return out, rows.Err()
}
