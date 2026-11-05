package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// UpdateUser overwrites the mutable fields with u. Changes based on the stored user go through ModifyUser,
// which cannot overwrite a concurrent change with stale fields.
func (r *Repository) UpdateUser(ctx context.Context, u *panel.User) error {
	return sqlite.Affected(r.db.ExecContext(ctx, updateUserQuery, updateUserArgs(u)...))
}
