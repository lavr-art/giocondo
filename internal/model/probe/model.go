// Package probe defines health probe requests and results.
package probe

import (
	"context"
	"errors"

	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// Request describes a single operation request.
type Request struct {
	WGPublicKey string           `json:"wg_public_key,omitempty"`
	IP          string           `json:"ip"`
	Host        string           `json:"host"`
	Inbound     protocol.Inbound `json:"inbound"`
	User        protocol.User    `json:"user"`
}

// Checker probes a single endpoint.
type Checker interface {
	Check(context.Context, Request) error
}

// CheckerFunc adapts a function to the Checker interface.
type CheckerFunc func(context.Context, Request) error

// Check performs the check.
func (f CheckerFunc) Check(ctx context.Context, r Request) error { return f(ctx, r) }

// ErrUnavailable is returned when a dependency is unavailable.
var ErrUnavailable = errors.New("probe point unavailable")
