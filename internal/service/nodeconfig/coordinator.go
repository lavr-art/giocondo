// Package nodeconfig coordinates node configuration updates.
package nodeconfig

import (
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// Coordinator issues WireGuard and monitor credentials for the services and wakes Sync when a new
// credential changes node configs.
type Coordinator struct {
	hooks     operation.Hooks
	nodes     MonitorStore
	users     UserSource
	allocator *Allocator
}

// NewCoordinator returns a credential coordinator; allocator must be the one config preparation uses.
func NewCoordinator(hooks operation.Hooks, nodes MonitorStore, users UserSource, allocator *Allocator) *Coordinator {
	return &Coordinator{hooks: hooks, nodes: nodes, users: users, allocator: allocator}
}
