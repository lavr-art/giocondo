package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"testing"
)

// captureDefault routes slog.Default to a JSON buffer for the test and restores it afterwards.
func captureDefault(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer

	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	return &buf
}

func record(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	var m map[string]any
	if err := json.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("decode %q: %v", buf.String(), err)
	}

	buf.Reset()

	return m
}

func TestFromCtxAddsStoredAttributes(t *testing.T) {
	buf := captureDefault(t)

	FromCtx(context.Background()).Info("plain")

	if m := record(t, buf); m["msg"] != "plain" || m["request_id"] != nil {
		t.Fatalf("record without attributes = %v", m)
	}

	parent := With(context.Background(), "request_id", "r1")
	child := With(parent, "node", "n1")

	FromCtx(child).Info("child")

	if m := record(t, buf); m["request_id"] != "r1" || m["node"] != "n1" {
		t.Fatalf("child record = %v, want request_id and node", m)
	}

	FromCtx(parent).Info("parent")

	if m := record(t, buf); m["request_id"] != "r1" || m["node"] != nil {
		t.Fatalf("parent record = %v, want only request_id", m)
	}
}

func TestFromCtxUsesCurrentDefault(t *testing.T) {
	ctx := With(context.Background(), "request_id", "r1")
	buf := captureDefault(t)

	FromCtx(ctx).Info("after SetDefault")

	if m := record(t, buf); m["request_id"] != "r1" {
		t.Fatalf("record = %v", m)
	}
}

func TestNewRequestID(t *testing.T) {
	a, b := NewRequestID(), NewRequestID()

	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(a) || a == b {
		t.Fatalf("request IDs %q, %q", a, b)
	}
}
