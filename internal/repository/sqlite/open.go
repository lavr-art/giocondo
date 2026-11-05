package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
)

// Open opens (or creates) the database and applies migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	// Check existing databases read-only, before journal_mode or other connection pragmas can write.
	if _, err := os.Stat(path); err == nil {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}

		uri := url.URL{Scheme: "file", Path: absolute, RawQuery: "mode=ro"}

		existing, err := sql.Open("sqlite", uri.String())
		if err != nil {
			return nil, err
		}

		err = checkSchema(ctx, existing)
		closeErr := existing.Close()

		if err != nil {
			return nil, err
		}

		if closeErr != nil {
			return nil, closeErr
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	q := url.Values{}
	for _, p := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
		q.Add("_pragma", p)
	}

	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}

	// One connection: writes are serialized anyway, and pragmas apply to everything at once.
	db.SetMaxOpenConns(1)
	s := &DB{DB: db}

	if err := s.migrate(ctx); err != nil {
		_ = db.Close()

		return nil, err
	}

	return s, nil
}

// OpenReadOnly opens an existing database without creating it or applying migrations.
// It lets operator checks inspect the state while the panel may still be migrating it.
func OpenReadOnly(ctx context.Context, path string) (*DB, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	q := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(1000)"}}
	uri := url.URL{Scheme: "file", Path: absolute, RawQuery: q.Encode()}

	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)

	if err := checkSchema(ctx, db); err != nil {
		_ = db.Close()

		return nil, err
	}

	return &DB{DB: db}, nil
}
