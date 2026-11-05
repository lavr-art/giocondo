// Package logger returns the process logger with the request-scoped attributes of a context.
//
// The process logger is slog.Default, set once in main. Boundaries (HTTP handlers, the agent stream,
// background operations) store correlation attributes with With; code that writes a log takes the
// logger with FromCtx, so every record of one request carries them.
package logger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
)

type attrsKey struct{}

// With returns a copy of ctx whose FromCtx loggers add args (slog key-value pairs) after the ones
// already stored. ctx itself is not changed.
func With(ctx context.Context, args ...any) context.Context {
	prev, _ := ctx.Value(attrsKey{}).([]any)

	attrs := make([]any, 0, len(prev)+len(args))
	attrs = append(attrs, prev...)
	attrs = append(attrs, args...)

	return context.WithValue(ctx, attrsKey{}, attrs)
}

// FromCtx returns slog.Default() with the attributes stored in ctx by With.
func FromCtx(ctx context.Context) *slog.Logger {
	attrs, _ := ctx.Value(attrsKey{}).([]any)
	if len(attrs) == 0 {
		return slog.Default()
	}

	return slog.Default().With(attrs...)
}

// NewRequestID returns a random 16-hex-character request identifier.
func NewRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error

	return hex.EncodeToString(b)
}
