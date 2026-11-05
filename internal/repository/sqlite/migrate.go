package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/migrations"
)

func migrationNames() ([]string, error) {
	names, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		return nil, err
	}

	sort.Strings(names)

	return names, nil
}

// checkSchema rejects a newer application's database before executing any migration.
func checkSchema(ctx context.Context, db *sql.DB) error {
	names, err := migrationNames()
	if err != nil {
		return err
	}

	var tableExists int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`,
	).Scan(&tableExists); err != nil {
		return err
	}

	if tableExists != 0 {
		var current int
		if err := db.QueryRowContext(
			ctx,
			`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`,
		).Scan(&current); err != nil {
			return err
		}

		latest, _, _ := strings.Cut(names[len(names)-1], "_")

		maximum, err := strconv.Atoi(latest)
		if err != nil {
			return err
		}

		if current > maximum {
			return fmt.Errorf(
				"database schema %d is newer than supported schema %d; restore matching packages and data",
				current,
				maximum,
			)
		}
	}

	return nil
}

func (db *DB) migrate(ctx context.Context) error {
	names, err := migrationNames()
	if err != nil {
		return err
	}

	if err := checkSchema(ctx, db.DB); err != nil {
		return err
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TIMESTAMP NOT NULL
	)`); err != nil {
		return fmt.Errorf("migrations table: %w", err)
	}

	for _, name := range names {
		num, _, _ := strings.Cut(name, "_")

		version, err := strconv.Atoi(num)
		if err != nil {
			return fmt.Errorf("migration %s: name must start with a number", name)
		}

		var exists int

		err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&exists)
		if err != nil {
			return err
		}

		if exists > 0 {
			continue
		}

		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return err
		}

		if err := db.InTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return err
			}

			_, err := tx.ExecContext(ctx,
				`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
				version, Time(time.Now()))

			return err
		}); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}

	return nil
}
