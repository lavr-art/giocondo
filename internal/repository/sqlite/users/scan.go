package users

import (
	"context"
	"database/sql"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
)

const userColumns = `id, name, token, uuid, traffic_limit_bytes, expires_at, enabled, created_at,
	wg_private_key, wg_public_key, wg_preshared_key, wg_address, usage_offset_up, usage_offset_down,
	last_status, warned_traffic, warned_expiry`

func scanUser(r sqlite.RowScanner) (*panel.User, error) {
	var (
		u                       panel.User
		created                 sqlite.Time
		wgPriv, wgPub, wgPSK, a sql.NullString
	)

	err := r.Scan(&u.ID, &u.Name, &u.Token, &u.UUID, &u.TrafficLimitBytes, sqlite.NullTime(&u.ExpiresAt), &u.Enabled,
		&created,
		&wgPriv, &wgPub, &wgPSK, &a, &u.UsageOffset.Up, &u.UsageOffset.Down,
		&u.Watch.LastStatus, &u.Watch.WarnedTraffic, &u.Watch.WarnedExpiry)
	if err != nil {
		return nil, sqlite.MapErr(err)
	}

	u.CreatedAt = time.Time(created)
	if wgPub.Valid {
		u.WG = &protocol.WGPeer{
			PrivateKey:   wgPriv.String,
			PublicKey:    wgPub.String,
			PresharedKey: wgPSK.String,
			Address:      a.String,
		}
	}

	return &u, nil
}

// wgArgs returns the WG column values; without keys NULL is written so UNIQUE(wg_address) is not affected.
func wgArgs(wg *protocol.WGPeer) []any {
	if wg == nil {
		return []any{nil, nil, nil, nil}
	}

	return []any{nullStr(wg.PrivateKey), nullStr(wg.PublicKey), nullStr(wg.PresharedKey), nullStr(wg.Address)}
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}

	return s
}

func (r *Repository) queryUsers(ctx context.Context, query string, args ...any) ([]panel.User, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, sqlite.MapErr(err)
	}
	defer func() { _ = rows.Close() }()

	var out []panel.User

	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, *u)
	}

	return out, rows.Err()
}

// updateUserQuery stores the mutable fields: name, UUID, limit, term, enabled and WG.
// The token changes only through RotateToken, CreatedAt never.
const updateUserQuery = `UPDATE users SET name = ?, uuid = ?, traffic_limit_bytes = ?, expires_at = ?, enabled = ?,
	wg_private_key = ?, wg_public_key = ?, wg_preshared_key = ?, wg_address = ? WHERE id = ?`

func updateUserArgs(u *panel.User) []any {
	args := []any{u.Name, u.UUID, u.TrafficLimitBytes, sqlite.NullableTime(u.ExpiresAt), u.Enabled}
	args = append(args, wgArgs(u.WG)...)

	return append(args, u.ID)
}
