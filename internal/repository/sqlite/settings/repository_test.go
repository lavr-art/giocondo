package settings

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
	"github.com/artemlavr/giocondo/migrations"
)

func TestSettingRoundTrip(t *testing.T) {
	ctx := context.Background()

	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	r := New(db)

	if _, err := r.GetSetting(ctx, "subscription"); !errors.Is(err, panel.ErrNotFound) {
		t.Fatalf("missing setting: %v, want ErrNotFound", err)
	}

	at := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, value := range []string{"first", "second"} {
		if err := r.PutSetting(ctx, "subscription", value, at); err != nil {
			t.Fatal(err)
		}

		got, err := r.GetSetting(ctx, "subscription")
		if err != nil {
			t.Fatal(err)
		}

		if got != value {
			t.Fatalf("GetSetting() = %q, want %q", got, value)
		}
	}

	var (
		rows      int
		updatedAt string
	)

	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), MAX(updated_at) FROM settings`).Scan(&rows, &updatedAt); err != nil {
		t.Fatal(err)
	}

	if rows != 1 || updatedAt != "2026-11-06T12:00:00Z" {
		t.Fatalf("rows = %d, updated_at = %q", rows, updatedAt)
	}
}

// A panel upgraded from the previous schema keeps its data and starts without stored settings.
func TestOpenUpgradesDatabaseWithoutSettings(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "panel.db")

	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := old.ExecContext(ctx,
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMP NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	names, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	for _, entry := range names {
		num, _, _ := strings.Cut(entry.Name(), "_")

		version, err := strconv.Atoi(num)
		if err != nil || version >= 7 {
			continue
		}

		body, err := migrations.FS.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}

		if _, err := old.ExecContext(ctx, string(body)); err != nil {
			t.Fatalf("%s: %v", entry.Name(), err)
		}

		if _, err := old.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, applied_at) VALUES (?, '2026-01-01T00:00:00Z')`, version); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := old.ExecContext(ctx,
		`INSERT INTO nodes (name, host, created_at) VALUES ('n1', 'n1.example.com', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	var nodes int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM nodes WHERE name = 'n1'`).Scan(&nodes); err != nil {
		t.Fatal(err)
	}

	if nodes != 1 {
		t.Fatalf("nodes after the upgrade = %d", nodes)
	}

	if _, err := New(db).GetSetting(ctx, "subscription"); !errors.Is(err, panel.ErrNotFound) {
		t.Fatalf("setting after the upgrade: %v, want ErrNotFound", err)
	}
}
