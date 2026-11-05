package engine

import (
	"crypto/sha256"
	"encoding/hex"
)

// Traffic counts bytes in both directions.
type Traffic struct{ Up, Down int64 }

// Hash returns the hex SHA-256 of data.
func Hash(data []byte) string {
	h := sha256.Sum256(data)

	return hex.EncodeToString(h[:])
}

// State is the engine state reported to the panel. AppliedHash is the confirmed config; RejectedHash and
// Error describe the last failed apply or recovery; Phase is ready, applying or blocked.
type State struct {
	ID           string
	Version      string
	Active       bool
	Phase        string
	AppliedHash  string
	RejectedHash string
	Error        string
}
