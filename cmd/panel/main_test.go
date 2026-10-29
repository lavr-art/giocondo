package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"", slog.LevelInfo},
		{"info", slog.LevelInfo},
		{"DEBUG", slog.LevelDebug},
		{" warn ", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"bogus", slog.LevelInfo},
	}
	for _, tt := range tests {
		if got := parseLevel(tt.in); got != tt.want {
			t.Errorf("parseLevel(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestTokenCommand(t *testing.T) {
	db := filepath.Join(t.TempDir(), "panel.db")
	var out bytes.Buffer
	if err := runToken([]string{"create", "-db", db, "-name", "ci", "-scope", "monitor"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "vpc_") {
		t.Errorf("create output %q", out.String())
	}
	out.Reset()
	if err := runToken([]string{"list", "-db", db}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ci") || strings.Contains(out.String(), "vpc_") {
		t.Errorf("list output %q", out.String())
	}
	if err := runToken([]string{"delete", "-db", db, "-id", "1"}, &out); err != nil {
		t.Fatal(err)
	}
	if err := runToken([]string{"create", "-db", db, "-name", "x", "-scope", "root"}, &out); err == nil {
		t.Error("bad scope accepted")
	}
}

func TestAdminHandler(t *testing.T) {
	inner := http.NewServeMux()
	inner.HandleFunc("GET /users", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "users") })
	srv := httptest.NewServer(adminHandler("/s3cret/", inner))
	defer srv.Close()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string) (int, string) {
		resp, err := noRedirect.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get("/"); code != http.StatusOK || !strings.Contains(body, "It works") {
		t.Errorf("/: %d %q", code, body)
	}
	for _, p := range []string{"/users", "/api/users", "/login", "/s3cretx/users"} {
		if code, _ := get(p); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", p, code)
		}
	}
	if code, body := get("/s3cret/users"); code != http.StatusOK || body != "users" {
		t.Errorf("/s3cret/users: %d %q", code, body)
	}
	if code, _ := get("/s3cret"); code != http.StatusMovedPermanently {
		t.Errorf("/s3cret: %d", code)
	}
	for in, want := range map[string]string{"": "", "/": "", "abc": "/abc", "/abc/": "/abc", " /a/b/ ": "/a/b"} {
		if got := normalizeBase(in); got != want {
			t.Errorf("normalizeBase(%q) = %q, want %q", in, got, want)
		}
	}
	if adminHandler("", inner) != http.Handler(inner) {
		t.Error("empty base must serve admin at root")
	}
}
