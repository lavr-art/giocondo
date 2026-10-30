package store

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFutureSchemaDoesNotChangeJournalMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMP); INSERT INTO schema_migrations VALUES (9999,NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), path)
	if s != nil {
		_ = s.Close()
	}
	if err == nil {
		t.Fatal("future schema accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("opening a future schema changed the database bytes")
	}
}

func TestFutureSchemaRejectedWithoutMutation(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.db.ExecContext(ctx, "INSERT INTO schema_migrations VALUES (9999, '2026-01-01T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Open(ctx, path)
	if next != nil {
		_ = next.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "newer") {
		t.Fatalf("Open future schema = %v, want newer-schema error", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=9999").Scan(&n); err != nil || n != 1 {
		t.Fatalf("future marker changed: %d, %v", n, err)
	}
}

func TestUpgradePopulatedInitialSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	initial, err := migrationsFS.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, string(initial)+`
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMP NOT NULL);
INSERT INTO schema_migrations VALUES (1, '2026-01-01T00:00:00Z');
INSERT INTO users (id,name,token,uuid,traffic_limit_bytes,expires_at,enabled,created_at,wg_private_key,wg_public_key,wg_preshared_key,wg_address)
VALUES (1,'alice','original-token','original-uuid',123456,'2027-01-01T00:00:00Z',1,'2026-01-01T00:00:00Z','private','public','preshared','10.70.0.2/32');
INSERT INTO nodes (id,name,host,created_at) VALUES (1,'original-node','vpn.example.com','2026-01-01T00:00:00Z');
INSERT INTO traffic VALUES (1,1,'2026-01-01',111,222);`)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		u, err := s.GetUser(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		if u.Name != "alice" || u.Token != "original-token" || u.UUID != "original-uuid" || u.TrafficLimitBytes != 123456 || !u.Enabled || u.ExpiresAt == nil || u.ExpiresAt.UTC().Format(time.RFC3339) != "2027-01-01T00:00:00Z" || u.WG == nil || u.WG.PrivateKey != "private" || u.WG.PublicKey != "public" || u.WG.PresharedKey != "preshared" || u.WG.Address != "10.70.0.2/32" {
			t.Fatalf("legacy user changed: %+v", u)
		}
		var up, down int64
		if err := s.db.QueryRow("SELECT up,down FROM traffic WHERE user_id=1 AND node_id=1").Scan(&up, &down); err != nil || up != 111 || down != 222 {
			t.Fatalf("traffic changed: %d/%d %v", up, down, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFailedMigrationRollsBackItsSchemaAndMarker(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conflict.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	initial, err := migrationsFS.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, string(initial)+`
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMP NOT NULL);
INSERT INTO schema_migrations VALUES (1, '2026-01-01T00:00:00Z');
CREATE INDEX hosts_inbound ON users (name);`)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, path)
	if s != nil {
		_ = s.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "0003_hosts") {
		t.Fatalf("expected conflicting migration error, got %v", err)
	}
	var tables, markers int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='hosts'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version=3").Scan(&markers); err != nil {
		t.Fatal(err)
	}
	if tables != 0 || markers != 0 {
		t.Fatalf("failed migration left table/marker: %d/%d", tables, markers)
	}
}
