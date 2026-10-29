package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/artemlavr/giocondo/internal/singbox"
)

const userColumns = `id, name, token, uuid, traffic_limit_bytes, expires_at, enabled, created_at,
	wg_private_key, wg_public_key, wg_preshared_key, wg_address, usage_offset_up, usage_offset_down,
	last_status, warned_traffic, warned_expiry`

type rowScanner interface{ Scan(dest ...any) error }

func scanUser(r rowScanner) (*User, error) {
	var (
		u                       User
		created                 dbTime
		wgPriv, wgPub, wgPSK, a sql.NullString
	)
	err := r.Scan(&u.ID, &u.Name, &u.Token, &u.UUID, &u.TrafficLimitBytes, nullTime{&u.ExpiresAt}, &u.Enabled, &created,
		&wgPriv, &wgPub, &wgPSK, &a, &u.UsageOffset.Up, &u.UsageOffset.Down,
		&u.Watch.LastStatus, &u.Watch.WarnedTraffic, &u.Watch.WarnedExpiry)
	if err != nil {
		return nil, mapErr(err)
	}
	u.CreatedAt = time.Time(created)
	if wgPub.Valid {
		u.WG = &singbox.WGPeer{PrivateKey: wgPriv.String, PublicKey: wgPub.String, PresharedKey: wgPSK.String, Address: a.String}
	}
	return &u, nil
}

// wgArgs returns the WG column values; without keys NULL is written so UNIQUE(wg_address) is not affected.
func wgArgs(wg *singbox.WGPeer) []any {
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

// CreateUser creates a user. Empty Token and UUID are generated, CreatedAt is the current time.
func (s *SQLite) CreateUser(ctx context.Context, u *User) error {
	if u.Name == "" {
		return fmt.Errorf("user name is required")
	}
	if u.Token == "" {
		t, err := NewToken()
		if err != nil {
			return err
		}
		u.Token = t
	}
	if u.UUID == "" {
		id, err := singbox.NewUUID()
		if err != nil {
			return err
		}
		u.UUID = id
	}
	u.CreatedAt = now()
	args := append([]any{u.Name, u.Token, u.UUID, u.TrafficLimitBytes, nullableTime(u.ExpiresAt), u.Enabled, dbTime(u.CreatedAt)},
		wgArgs(u.WG)...)
	err := s.db.QueryRowContext(ctx, `INSERT INTO users (name, token, uuid, traffic_limit_bytes, expires_at, enabled, created_at,
		wg_private_key, wg_public_key, wg_preshared_key, wg_address)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`, args...).Scan(&u.ID)
	return mapErr(err)
}

// GetUser returns a user by id.
func (s *SQLite) GetUser(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// GetUserByToken returns a user by subscription token.
func (s *SQLite) GetUserByToken(ctx context.Context, token string) (*User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE token = ?`, token))
}

// ListUsers returns all users in ascending id order.
func (s *SQLite) ListUsers(ctx context.Context) ([]User, error) {
	return s.queryUsers(ctx, `SELECT `+userColumns+` FROM users ORDER BY id`)
}

func (s *SQLite) queryUsers(ctx context.Context, query string, args ...any) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = rows.Close() }()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// UpdateUser stores the mutable fields: name, UUID, limit, term, enabled and WG.
// The token changes only through RotateToken, CreatedAt never.
func (s *SQLite) UpdateUser(ctx context.Context, u *User) error {
	args := append([]any{u.Name, u.UUID, u.TrafficLimitBytes, nullableTime(u.ExpiresAt), u.Enabled}, wgArgs(u.WG)...)
	args = append(args, u.ID)
	return affected(s.db.ExecContext(ctx, `UPDATE users SET name = ?, uuid = ?, traffic_limit_bytes = ?, expires_at = ?, enabled = ?,
		wg_private_key = ?, wg_public_key = ?, wg_preshared_key = ?, wg_address = ? WHERE id = ?`, args...))
}

// SetUserWatch stores the watcher state for a user.
func (s *SQLite) SetUserWatch(ctx context.Context, id int64, w UserWatch) error {
	return affected(s.db.ExecContext(ctx, `UPDATE users SET last_status = ?, warned_traffic = ?, warned_expiry = ? WHERE id = ?`,
		w.LastStatus, w.WarnedTraffic, w.WarnedExpiry, id))
}

// DeleteUser deletes a user together with their traffic.
func (s *SQLite) DeleteUser(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id))
}

// RotateToken gives the user a new subscription token; the old link stops working.
func (s *SQLite) RotateToken(ctx context.Context, id int64) (string, error) {
	t, err := NewToken()
	if err != nil {
		return "", err
	}
	if err := affected(s.db.ExecContext(ctx, `UPDATE users SET token = ? WHERE id = ?`, t, id)); err != nil {
		return "", err
	}
	return t, nil
}

// ActiveUsers returns users allowed to connect: enabled, term not expired,
// used traffic (since the last ResetUsage) below the limit (limit 0 means unlimited).
func (s *SQLite) ActiveUsers(ctx context.Context, at time.Time) ([]User, error) {
	return s.queryUsers(ctx, `SELECT `+userColumns+` FROM users
		WHERE enabled
		  AND (expires_at IS NULL OR expires_at > ?)
		  AND (traffic_limit_bytes = 0 OR traffic_limit_bytes >
		       (SELECT COALESCE(SUM(up + down), 0) FROM traffic WHERE traffic.user_id = users.id)
		       - usage_offset_up - usage_offset_down)
		ORDER BY id`, dbTime(at))
}

// UserUsage is a user's traffic since the last ResetUsage.
func (s *SQLite) UserUsage(ctx context.Context, userID int64) (Usage, error) {
	var u Usage
	err := s.db.QueryRowContext(ctx, `SELECT
		COALESCE((SELECT SUM(up) FROM traffic WHERE user_id = users.id), 0) - usage_offset_up,
		COALESCE((SELECT SUM(down) FROM traffic WHERE user_id = users.id), 0) - usage_offset_down
		FROM users WHERE id = ?`, userID).Scan(&u.Up, &u.Down)
	return u.clamp(), mapErr(err)
}

// UsageAll is UserUsage for all users in one query.
func (s *SQLite) UsageAll(ctx context.Context) (map[int64]Usage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT users.id,
		COALESCE(SUM(traffic.up), 0) - users.usage_offset_up,
		COALESCE(SUM(traffic.down), 0) - users.usage_offset_down
		FROM users LEFT JOIN traffic ON traffic.user_id = users.id
		GROUP BY users.id, users.usage_offset_up, users.usage_offset_down`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]Usage{}
	for rows.Next() {
		var (
			id int64
			u  Usage
		)
		if err := rows.Scan(&id, &u.Up, &u.Down); err != nil {
			return nil, err
		}
		out[id] = u.clamp()
	}
	return out, rows.Err()
}

// ResetUsage resets a user's used traffic; the history in traffic remains.
func (s *SQLite) ResetUsage(ctx context.Context, userID int64) error {
	return affected(s.db.ExecContext(ctx, `UPDATE users SET
		usage_offset_up = COALESCE((SELECT SUM(up) FROM traffic WHERE user_id = users.id), 0),
		usage_offset_down = COALESCE((SELECT SUM(down) FROM traffic WHERE user_id = users.id), 0)
		WHERE id = ?`, userID))
}
