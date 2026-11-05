// Package hosts manages hostname overrides for inbounds.
package hosts

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// Service manages host overrides. Overrides change only subscriptions, not node configs, so they
// do not wake Sync.
type Service struct {
	hooks operation.Hooks
	hosts HostRepository
	nodes NodeRepository
}

// New returns a host override service.
func New(hooks operation.Hooks, hosts HostRepository, nodes NodeRepository) *Service {
	return &Service{hooks: hooks, hosts: hosts, nodes: nodes}
}

// HostRepository stores inbound host overrides.
type HostRepository interface {
	CreateHost(ctx context.Context, h *panel.Host) error
	DeleteHost(ctx context.Context, id int64) error
	GetHost(ctx context.Context, id int64) (*panel.Host, error)
	ListHosts(ctx context.Context) ([]panel.Host, error)
	UpdateHost(ctx context.Context, h *panel.Host) error
}

// NodeRepository reads the nodes and inbounds hosts belong to.
type NodeRepository interface {
	GetNode(ctx context.Context, id int64) (*panel.Node, error)
	ListInbounds(ctx context.Context, nodeID int64) ([]panel.Inbound, error)
	ListNodes(ctx context.Context) ([]panel.Node, error)
}
