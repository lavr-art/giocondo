package tokens

import (
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

//nolint:gosec // a SQL column list, not a credential.
const tokenColumns = `id, name, scope, token_hash, created_at, last_used_at`

func scanToken(r sqlite.RowScanner) (*panel.APIToken, error) {
	var (
		t       panel.APIToken
		created sqlite.Time
	)
	if err := r.Scan(&t.ID, &t.Name, &t.Scope, &t.Hash, &created, sqlite.NullTime(&t.LastUsedAt)); err != nil {
		return nil, sqlite.MapErr(err)
	}

	t.CreatedAt = time.Time(created)

	return &t, nil
}
