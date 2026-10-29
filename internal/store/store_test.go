package store

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/singbox"
)

func openTest(t *testing.T) (*SQLite, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "panel.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func mustUser(t *testing.T, s *SQLite, u User) *User {
	t.Helper()
	if err := s.CreateUser(context.Background(), &u); err != nil {
		t.Fatal(err)
	}
	return &u
}

func mustNode(t *testing.T, s *SQLite, n Node) *Node {
	t.Helper()
	if err := s.CreateNode(context.Background(), &n); err != nil {
		t.Fatal(err)
	}
	return &n
}

func wgPeer(t *testing.T, addr string) *singbox.WGPeer {
	t.Helper()
	priv, pub, err := singbox.NewWGKeypair()
	if err != nil {
		t.Fatal(err)
	}
	psk, err := singbox.NewPresharedKey()
	if err != nil {
		t.Fatal(err)
	}
	return &singbox.WGPeer{PrivateKey: priv, PublicKey: pub, PresharedKey: psk, Address: addr}
}

func TestMigrationsIdempotent(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	mustUser(t, s, User{Name: "alice", Enabled: true})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	var versions int
	if err := s2.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	files, _ := fs.Glob(migrationsFS, "migrations/*.sql")
	if versions != len(files) || versions < 2 {
		t.Errorf("schema_migrations has %d rows, want %d", versions, len(files))
	}
	users, err := s2.ListUsers(ctx)
	if err != nil || len(users) != 1 {
		t.Fatalf("data lost after reopen: %v, %v", users, err)
	}
	var fk int
	if err := s2.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys = %d, %v", fk, err)
	}
}

func TestUserCRUD(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	exp := time.Date(2027, 1, 2, 3, 4, 5, 0, time.FixedZone("MSK", 3*3600))
	u := mustUser(t, s, User{Name: "alice", TrafficLimitBytes: 100 << 30, ExpiresAt: &exp, Enabled: true, WG: wgPeer(t, "10.70.0.2/32")})

	if !regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`).MatchString(u.Token) {
		t.Errorf("token %q is not 32 bytes of base64url", u.Token)
	}
	if u.ID == 0 || u.UUID == "" || u.CreatedAt.IsZero() {
		t.Errorf("generated fields not set: %+v", u)
	}

	got, err := s.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "alice" || got.Token != u.Token || got.UUID != u.UUID || got.TrafficLimitBytes != 100<<30 || !got.Enabled {
		t.Errorf("got %+v", got)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) || got.ExpiresAt.Location() != time.UTC {
		t.Errorf("expires_at = %v, want %v in UTC", got.ExpiresAt, exp)
	}
	if got.CreatedAt.Location() != time.UTC || !got.CreatedAt.Equal(u.CreatedAt) {
		t.Errorf("created_at = %v, want %v", got.CreatedAt, u.CreatedAt)
	}
	if got.WG == nil || *got.WG != *u.WG {
		t.Errorf("wg = %+v, want %+v", got.WG, u.WG)
	}

	byToken, err := s.GetUserByToken(ctx, u.Token)
	if err != nil || byToken.ID != u.ID {
		t.Fatalf("by token: %v, %v", byToken, err)
	}

	got.Name, got.TrafficLimitBytes, got.ExpiresAt, got.Enabled, got.WG = "alice2", 0, nil, false, nil
	if err := s.UpdateUser(ctx, got); err != nil {
		t.Fatal(err)
	}
	upd, err := s.GetUser(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if upd.Name != "alice2" || upd.ExpiresAt != nil || upd.Enabled || upd.WG != nil || upd.Token != u.Token {
		t.Errorf("after update: %+v", upd)
	}

	mustUser(t, s, User{Name: "bob"})
	list, err := s.ListUsers(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "alice2" || list[1].Name != "bob" {
		t.Fatalf("list = %+v, %v", list, err)
	}

	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetUser(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get deleted: %v", err)
	}
	if err := s.DeleteUser(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	if err := s.UpdateUser(ctx, &User{ID: 9999, Name: "x", UUID: "y"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing: %v", err)
	}
	if _, err := s.GetUserByToken(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown token: %v", err)
	}
}

func TestUserConflicts(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	a := mustUser(t, s, User{Name: "alice", WG: wgPeer(t, "10.70.0.2/32")})

	tests := []struct {
		name string
		u    User
	}{
		{"name", User{Name: "alice"}},
		{"token", User{Name: "b", Token: a.Token}},
		{"uuid", User{Name: "c", UUID: a.UUID}},
		{"wg address", User{Name: "d", WG: wgPeer(t, "10.70.0.2/32")}},
	}
	for _, tt := range tests {
		if err := s.CreateUser(ctx, &tt.u); !errors.Is(err, ErrConflict) {
			t.Errorf("%s: err = %v, want ErrConflict", tt.name, err)
		}
	}
	// Users without WG do not conflict on a NULL address.
	mustUser(t, s, User{Name: "e"})
	mustUser(t, s, User{Name: "f"})
}

func TestRotateToken(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	u := mustUser(t, s, User{Name: "alice"})
	tok, err := s.RotateToken(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tok == u.Token || len(tok) != 43 {
		t.Errorf("new token %q", tok)
	}
	if _, err := s.GetUserByToken(ctx, u.Token); !errors.Is(err, ErrNotFound) {
		t.Errorf("old token still works: %v", err)
	}
	if got, err := s.GetUserByToken(ctx, tok); err != nil || got.ID != u.ID {
		t.Errorf("new token: %v, %v", got, err)
	}
	if _, err := s.RotateToken(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("rotate missing: %v", err)
	}
}

func TestNodesAndInbounds(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	n := mustNode(t, s, Node{Name: "n1", Host: "n1.example.com", IP: "203.0.113.10", Region: "fsn1"})
	if n.Status != NodeNew {
		t.Errorf("default status %q", n.Status)
	}
	if err := s.CreateNode(ctx, &Node{Name: "n1", Host: "x"}); !errors.Is(err, ErrConflict) {
		t.Errorf("dup node: %v", err)
	}

	seen := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	if err := s.TouchNode(ctx, n.ID, NodeOnline, seen); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetNode(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != NodeOnline || got.LastSeen == nil || !got.LastSeen.Equal(seen) || got.Region != "fsn1" {
		t.Errorf("after touch: %+v", got)
	}
	got.Host = "n1b.example.com"
	if err := s.UpdateNode(ctx, got); err != nil {
		t.Fatal(err)
	}
	if nodes, err := s.ListNodes(ctx); err != nil || len(nodes) != 1 || nodes[0].Host != "n1b.example.com" {
		t.Fatalf("list nodes = %+v, %v", nodes, err)
	}

	reality := &Inbound{NodeID: n.ID, Spec: singbox.Inbound{Type: singbox.TypeVLESSReality, Port: 443, Reality: &singbox.RealityParams{
		PrivateKey: "k", ShortIDs: []string{"abcd"}, Dest: "www.microsoft.com:443", ServerName: "www.microsoft.com",
	}}}
	if err := s.CreateInbound(ctx, reality); err != nil {
		t.Fatal(err)
	}
	if reality.Spec.Tag != "vless-reality-443" {
		t.Errorf("default tag %q", reality.Spec.Tag)
	}
	hy2 := &Inbound{NodeID: n.ID, Spec: singbox.Inbound{Type: singbox.TypeHysteria2, Tag: "hy2", Port: 8443,
		Hysteria2: &singbox.Hysteria2Params{TLS: singbox.TLSACME, ACMEEmail: "a@example.com", UpMbps: 100}}}
	if err := s.CreateInbound(ctx, hy2); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateInbound(ctx, &Inbound{NodeID: n.ID, Spec: singbox.Inbound{Type: singbox.TypeWireGuard, Tag: "hy2", Port: 51820}}); !errors.Is(err, ErrConflict) {
		t.Errorf("dup tag: %v", err)
	}
	if err := s.CreateInbound(ctx, &Inbound{NodeID: 9999, Spec: singbox.Inbound{Type: singbox.TypeWireGuard, Port: 51820}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing node: %v", err)
	}
	if err := s.CreateInbound(ctx, &Inbound{NodeID: n.ID, Spec: singbox.Inbound{Type: "socks", Port: 1080}}); err == nil {
		t.Error("unknown type accepted")
	}

	ins, err := s.ListInbounds(ctx, n.ID)
	if err != nil || len(ins) != 2 {
		t.Fatalf("inbounds = %+v, %v", ins, err)
	}
	if r := ins[0].Spec.Reality; r == nil || r.PrivateKey != "k" || r.ShortIDs[0] != "abcd" || r.ServerName != "www.microsoft.com" {
		t.Errorf("reality params = %+v", r)
	}
	if h := ins[1].Spec.Hysteria2; h == nil || h.TLS != singbox.TLSACME || h.UpMbps != 100 || ins[1].Spec.Reality != nil {
		t.Errorf("hysteria2 params = %+v", ins[1].Spec)
	}

	ins[1].Spec.Port = 9443
	ins[1].Spec.Hysteria2.UpMbps = 50
	if err := s.UpdateInbound(ctx, &ins[1]); err != nil {
		t.Fatal(err)
	}
	ins2, _ := s.ListInbounds(ctx, n.ID)
	if ins2[1].Spec.Port != 9443 || ins2[1].Spec.Hysteria2.UpMbps != 50 {
		t.Errorf("after update: %+v", ins2[1].Spec)
	}
	if err := s.DeleteInbound(ctx, ins2[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteInbound(ctx, ins2[0].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}

func TestCascadeDelete(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	u := mustUser(t, s, User{Name: "alice", Enabled: true})
	n := mustNode(t, s, Node{Name: "n1", Host: "h"})
	if err := s.CreateInbound(ctx, &Inbound{NodeID: n.ID, Spec: singbox.Inbound{Type: singbox.TypeWireGuard, Port: 51820, WireGuard: &singbox.WireGuardParams{}}}); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	if err := s.AddTraffic(ctx, []TrafficDelta{{UserID: u.ID, NodeID: n.ID, At: at, Up: 1, Down: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNode(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if ins, _ := s.ListInbounds(ctx, n.ID); len(ins) != 0 {
		t.Errorf("inbounds survived node deletion: %v", ins)
	}
	if usage, _ := s.UsageByUser(ctx, u.ID, at, at); usage.Total() != 0 {
		t.Errorf("traffic survived node deletion: %+v", usage)
	}
}

func TestTraffic(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	u := mustUser(t, s, User{Name: "alice"})
	other := mustUser(t, s, User{Name: "bob"})
	n1 := mustNode(t, s, Node{Name: "n1", Host: "h1"})
	n2 := mustNode(t, s, Node{Name: "n2", Host: "h2"})
	d1 := time.Date(2026, 10, 1, 23, 59, 0, 0, time.UTC)
	d2 := time.Date(2026, 10, 2, 0, 1, 0, 0, time.UTC)
	// October 2, 01:00 Moscow time is still October 1 in UTC.
	d1msk := time.Date(2026, 10, 2, 1, 0, 0, 0, time.FixedZone("MSK", 3*3600))

	err := s.AddTraffic(ctx, []TrafficDelta{
		{UserID: u.ID, NodeID: n1.ID, At: d1, Up: 10, Down: 100},
		{UserID: u.ID, NodeID: n1.ID, At: d1msk, Up: 5, Down: 50},
		{UserID: u.ID, NodeID: n2.ID, At: d1, Up: 1, Down: 1},
		{UserID: u.ID, NodeID: n1.ID, At: d2, Up: 7, Down: 70},
		{UserID: other.ID, NodeID: n1.ID, At: d1, Up: 1000, Down: 1000},
		{UserID: u.ID, NodeID: n1.ID, At: d2}, // a zero delta is skipped
	})
	if err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM traffic WHERE user_id = ?`, u.ID).Scan(&rows); err != nil || rows != 3 {
		t.Errorf("traffic rows for alice = %d, %v; want 3 (n1/day1, n2/day1, n1/day2)", rows, err)
	}

	tests := []struct {
		from, to time.Time
		want     Usage
	}{
		{d1, d1, Usage{Up: 16, Down: 151}},
		{d2, d2, Usage{Up: 7, Down: 70}},
		{d1, d2, Usage{Up: 23, Down: 221}},
		{d2.AddDate(0, 0, 1), d2.AddDate(0, 0, 5), Usage{}},
	}
	for _, tt := range tests {
		got, err := s.UsageByUser(ctx, u.ID, tt.from, tt.to)
		if err != nil || got != tt.want {
			t.Errorf("usage %s..%s = %+v, %v; want %+v", day(tt.from), day(tt.to), got, err, tt.want)
		}
	}

	if err := s.AddTraffic(ctx, []TrafficDelta{{UserID: u.ID, NodeID: n1.ID, At: d1, Up: -1}}); err == nil {
		t.Error("negative delta accepted")
	}
	// An error in the middle of a batch rolls back the whole batch.
	err = s.AddTraffic(ctx, []TrafficDelta{
		{UserID: u.ID, NodeID: n1.ID, At: d1, Up: 1},
		{UserID: 9999, NodeID: n1.ID, At: d1, Up: 1},
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown user: %v", err)
	}
	if got, _ := s.UsageByUser(ctx, u.ID, d1, d1); got.Up != 16 {
		t.Errorf("partial batch was committed: %+v", got)
	}
}

func TestActiveUsers(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	past, future := at.Add(-time.Second), at.Add(time.Hour)
	n := mustNode(t, s, Node{Name: "n1", Host: "h"})

	ok := mustUser(t, s, User{Name: "ok", Enabled: true})
	mustUser(t, s, User{Name: "disabled", Enabled: false})
	mustUser(t, s, User{Name: "expired", Enabled: true, ExpiresAt: &past})
	notYet := mustUser(t, s, User{Name: "not-expired", Enabled: true, ExpiresAt: &future})
	over := mustUser(t, s, User{Name: "over-limit", Enabled: true, TrafficLimitBytes: 100})
	under := mustUser(t, s, User{Name: "under-limit", Enabled: true, TrafficLimitBytes: 100})
	unlimited := mustUser(t, s, User{Name: "unlimited", Enabled: true})

	err := s.AddTraffic(ctx, []TrafficDelta{
		{UserID: over.ID, NodeID: n.ID, At: at, Up: 60, Down: 40}, // exactly at the limit is already not allowed
		{UserID: under.ID, NodeID: n.ID, At: at, Up: 60, Down: 39},
		{UserID: unlimited.ID, NodeID: n.ID, At: at, Up: 1 << 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	active, err := s.ActiveUsers(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, u := range active {
		names = append(names, u.Name)
	}
	want := []string{ok.Name, notYet.Name, under.Name, unlimited.Name}
	if len(names) != len(want) {
		t.Fatalf("active = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("active = %v, want %v", names, want)
		}
	}
	// At the moment of expiry the user is already inactive.
	if act, _ := s.ActiveUsers(ctx, future); len(act) != 3 {
		t.Errorf("at expiry: %d active, want 3", len(act))
	}
}

func TestNodeConfigInput(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	n := mustNode(t, s, Node{Name: "n1", Host: "n1.example.com"})
	spec := singbox.Node{Host: n.Host, Inbounds: []singbox.Inbound{
		{Type: singbox.TypeVLESSReality, Tag: "vless", Port: 443, Reality: &singbox.RealityParams{Dest: "www.microsoft.com:443", ServerName: "www.microsoft.com"}},
		{Type: singbox.TypeHysteria2, Tag: "hy2", Port: 8443},
		{Type: singbox.TypeWireGuard, Tag: "wg", Port: 51820},
	}}
	users := []singbox.User{{Name: "alice"}, {Name: "bob"}}
	if err := singbox.Fill(&spec, users); err != nil {
		t.Fatal(err)
	}
	for _, in := range spec.Inbounds {
		if err := s.CreateInbound(ctx, &Inbound{NodeID: n.ID, Spec: in}); err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range users {
		mustUser(t, s, User{Name: u.Name, UUID: u.UUID, Enabled: true, WG: u.WG})
	}
	mustUser(t, s, User{Name: "off", Enabled: false})

	node, active, err := NodeConfigInput(ctx, s, n.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if node.ID != "n1" || len(node.Inbounds) != 3 || len(active) != 2 {
		t.Fatalf("node %+v, users %+v", node, active)
	}
	if _, err := singbox.Build(node, active, singbox.Options{}); err != nil {
		t.Fatalf("config from store does not build: %v", err)
	}
	if _, _, err := NodeConfigInput(ctx, s, 9999, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing node: %v", err)
	}
}

func TestDBTimeScan(t *testing.T) {
	want := time.Date(2026, 10, 28, 12, 30, 0, 0, time.UTC)
	for _, src := range []any{
		"2026-10-28T12:30:00Z",
		[]byte("2026-10-28T12:30:00Z"),
		"2026-10-28 15:30:00+03:00",
		"2026-10-28 12:30:00",
		want.In(time.FixedZone("X", 7200)),
	} {
		var v dbTime
		if err := v.Scan(src); err != nil {
			t.Errorf("%v: %v", src, err)
			continue
		}
		if got := time.Time(v); !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("%v: got %v", src, got)
		}
	}
	var v dbTime
	if err := v.Scan(42); err == nil {
		t.Error("int accepted")
	}
	if err := v.Scan("yesterday"); err == nil {
		t.Error("garbage accepted")
	}
}

func TestResetUsage(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	n := mustNode(t, s, Node{Name: "n1", Host: "h"})
	u := mustUser(t, s, User{Name: "alice", Enabled: true, TrafficLimitBytes: 100})
	other := mustUser(t, s, User{Name: "bob", Enabled: true})
	add := func(id, up, down int64) {
		t.Helper()
		if err := s.AddTraffic(ctx, []TrafficDelta{{UserID: id, NodeID: n.ID, At: at, Up: up, Down: down}}); err != nil {
			t.Fatal(err)
		}
	}
	add(u.ID, 60, 50)
	add(other.ID, 5, 5)
	if got, _ := s.UserUsage(ctx, u.ID); got != (Usage{Up: 60, Down: 50}) {
		t.Fatalf("usage %+v", got)
	}
	if act, _ := s.ActiveUsers(ctx, at); len(act) != 1 || act[0].Name != "bob" {
		t.Fatalf("over-limit user is active: %+v", act)
	}

	if err := s.ResetUsage(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.UserUsage(ctx, u.ID); got.Total() != 0 {
		t.Errorf("after reset %+v", got)
	}
	if act, _ := s.ActiveUsers(ctx, at); len(act) != 2 {
		t.Errorf("reset user is still inactive: %d active", len(act))
	}
	// History is not deleted, new traffic counts from zero.
	if total, _ := s.UsageByUser(ctx, u.ID, at, at); total.Total() != 110 {
		t.Errorf("history lost: %+v", total)
	}
	add(u.ID, 7, 3)
	all, err := s.UsageAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if all[u.ID] != (Usage{Up: 7, Down: 3}) || all[other.ID] != (Usage{Up: 5, Down: 5}) {
		t.Errorf("usage all %+v", all)
	}
	got, _ := s.GetUser(ctx, u.ID)
	if got.UsageOffset != (Usage{Up: 60, Down: 50}) {
		t.Errorf("offset %+v", got.UsageOffset)
	}
	// Deleting a node deletes its history: used traffic does not go negative.
	if err := s.DeleteNode(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.UserUsage(ctx, u.ID); got != (Usage{}) {
		t.Errorf("negative usage after node deletion: %+v", got)
	}
	if err := s.ResetUsage(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("reset missing: %v", err)
	}
	if _, err := s.UserUsage(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("usage missing: %v", err)
	}
}

func TestHosts(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	n := mustNode(t, s, Node{Name: "n1", Host: "n1.example.com"})
	in := &Inbound{NodeID: n.ID, Spec: singbox.Inbound{Type: singbox.TypeHysteria2, Tag: "hy2", Port: 8443, Hysteria2: &singbox.Hysteria2Params{}}}
	if err := s.CreateInbound(ctx, in); err != nil {
		t.Fatal(err)
	}
	b := &Host{InboundID: in.ID, Address: "b.example.com", Sort: 2}
	a := &Host{InboundID: in.ID, Address: "203.0.113.5", Port: 443, SNI: "n1.example.com", Sort: 1}
	for _, h := range []*Host{b, a} {
		if err := s.CreateHost(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.CreateHost(ctx, &Host{InboundID: 9999, Address: "x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("host for missing inbound: %v", err)
	}
	if err := s.CreateHost(ctx, &Host{InboundID: in.ID}); err == nil {
		t.Error("empty address accepted")
	}
	list, err := s.ListHosts(ctx)
	if err != nil || len(list) != 2 || list[0].Address != "203.0.113.5" || list[0].Port != 443 || list[0].SNI != "n1.example.com" {
		t.Fatalf("list %+v, %v", list, err)
	}
	a.Path, a.Sort = "/ws", 5
	if err := s.UpdateHost(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetHost(ctx, a.ID); got.Path != "/ws" || got.Sort != 5 {
		t.Errorf("after update %+v", got)
	}
	if list, _ = s.ListHosts(ctx); list[0].ID != b.ID {
		t.Error("sort order ignored")
	}
	if err := s.DeleteHost(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHost(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
	// Deleting an inbound deletes its addresses too.
	if err := s.DeleteInbound(ctx, in.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ = s.ListHosts(ctx); len(list) != 0 {
		t.Errorf("hosts survived inbound deletion: %+v", list)
	}
}
