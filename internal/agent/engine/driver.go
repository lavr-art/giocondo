// Package engine manages external VPN services without importing their cores.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

type Traffic struct{ Up, Down int64 }

// Driver owns engine-specific commands. Runtime owns persistence and ordering.
// All methods must honor context cancellation and must not log configuration secrets.
type Driver interface {
	ID() string
	Version(context.Context) (string, error)
	Validate(context.Context, string) error
	Restart(context.Context) error
	Stop(context.Context) error
	Active(context.Context) (bool, error)
	Collect(context.Context) (map[string]Traffic, error)
}

func Hash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }

type State struct {
	ID           string
	Version      string
	Active       bool
	Phase        string
	AppliedHash  string
	RejectedHash string
	Error        string
}
