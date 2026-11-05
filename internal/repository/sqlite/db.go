// Package sqlite opens the panel database and holds the helpers shared by the domain repositories
// in its subdirectories.
package sqlite

import (
	"context"
	"database/sql"
)

// DB is the panel database connection shared by the domain repositories.
type DB struct {
	*sql.DB
}

// InTx runs fn in a transaction and commits it if fn succeeds.
func (db *DB) InTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit()
}
