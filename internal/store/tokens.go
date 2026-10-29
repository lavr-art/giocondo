package store

import (
	"context"
	"fmt"
	"time"
)

const tokenColumns = `id, name, scope, token_hash, created_at, last_used_at`

func scanToken(r rowScanner) (*APIToken, error) {
	var (
		t       APIToken
		created dbTime
	)
	if err := r.Scan(&t.ID, &t.Name, &t.Scope, &t.Hash, &created, nullTime{&t.LastUsedAt}); err != nil {
		return nil, mapErr(err)
	}
	t.CreatedAt = time.Time(created)
	return &t, nil
}

// CreateToken stores an API token (Hash is the token sha256). CreatedAt is the current time.
func (s *SQLite) CreateToken(ctx context.Context, t *APIToken) error {
	if t.Name == "" || t.Scope == "" || t.Hash == "" {
		return fmt.Errorf("token name, scope and hash are required")
	}
	t.CreatedAt = now()
	return mapErr(s.db.QueryRowContext(ctx, `INSERT INTO api_tokens (name, scope, token_hash, created_at)
		VALUES (?, ?, ?, ?) RETURNING id`, t.Name, t.Scope, t.Hash, dbTime(t.CreatedAt)).Scan(&t.ID))
}

// ListTokens returns all tokens in ascending id order.
func (s *SQLite) ListTokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+tokenColumns+` FROM api_tokens ORDER BY id`)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = rows.Close() }()
	var out []APIToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

// TokenByHash finds a token by sha256.
func (s *SQLite) TokenByHash(ctx context.Context, hash string) (*APIToken, error) {
	return scanToken(s.db.QueryRowContext(ctx, `SELECT `+tokenColumns+` FROM api_tokens WHERE token_hash = ?`, hash))
}

// TouchToken records the time of last use.
func (s *SQLite) TouchToken(ctx context.Context, id int64, at time.Time) error {
	return affected(s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, dbTime(at), id))
}

// DeleteToken revokes a token.
func (s *SQLite) DeleteToken(ctx context.Context, id int64) error {
	return affected(s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE id = ?`, id))
}
