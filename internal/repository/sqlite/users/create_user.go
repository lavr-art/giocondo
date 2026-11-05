package users

import (
	"context"
	"fmt"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

// CreateUser creates a user. Empty Token and UUID are generated, CreatedAt is the current time.
func (r *Repository) CreateUser(ctx context.Context, u *panel.User) error {
	if u.Name == "" {
		return fmt.Errorf("user name is required")
	}

	if u.Token == "" {
		t, err := sqlite.NewToken()
		if err != nil {
			return err
		}

		u.Token = t
	}

	if u.UUID == "" {
		id, err := protocol.NewUUID()
		if err != nil {
			return err
		}

		u.UUID = id
	}

	u.CreatedAt = sqlite.Now()
	args := append([]any{
		u.Name,
		u.Token,
		u.UUID,
		u.TrafficLimitBytes,
		sqlite.NullableTime(u.ExpiresAt),
		u.Enabled,
		sqlite.Time(u.CreatedAt),
	},
		wgArgs(u.WG)...)
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO users (name, token, uuid, traffic_limit_bytes, expires_at, enabled, created_at,
		wg_private_key, wg_public_key, wg_preshared_key, wg_address)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		args...).Scan(&u.ID)

	return sqlite.MapErr(err)
}
