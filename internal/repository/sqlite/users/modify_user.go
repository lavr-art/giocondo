package users

import (
	"context"
	"database/sql"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// ModifyUser reads the user, applies change and stores the mutable fields in one transaction, so
// concurrent changes of the same user do not overwrite each other. The store has a single connection,
// which the transaction holds: change must not call the store. If change fails, nothing is stored and
// its error is returned as is. The result is the stored user.
func (r *Repository) ModifyUser(ctx context.Context, id int64, change func(*panel.User) error) (*panel.User, error) {
	var u *panel.User

	err := r.db.InTx(ctx, func(tx *sql.Tx) error {
		var err error

		u, err = scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
		if err != nil {
			return err
		}

		if err := change(u); err != nil {
			return err
		}

		return sqlite.Affected(tx.ExecContext(ctx, updateUserQuery, updateUserArgs(u)...))
	})
	if err != nil {
		return nil, err
	}

	return u, nil
}
