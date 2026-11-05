// Package nodes manages node lifecycle.
package nodes

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// credentialIssuer issues the WireGuard peers and monitor credentials nodes need.
type credentialIssuer interface {
	EnsureWG(ctx context.Context) error
	EnsureMonitor(ctx context.Context, nodeID int64) (*protocol.User, error)
}

// networkLock reports an error while a network operation owns the node state.
type networkLock interface {
	Unlocked(ctx context.Context, nodeID int64) error
}

// nodeViewer builds the API view of a node.
type nodeViewer interface {
	GetNode(ctx context.Context, id int64) (panel.NodeView, error)
}

// hostLister lists the host overrides of a node.
type hostLister interface {
	ListHosts(ctx context.Context, nodeID int64) ([]panel.HostView, error)
}

// Service changes nodes and wakes Sync when their desired state changes.
type Service struct {
	hooks             operation.Hooks
	nodes             NodeOperationRepository
	nodeLock          *operation.NodeStateLock
	monitoringEnabled bool
	credentials       credentialIssuer
	network           networkLock
	views             nodeViewer
}

// New returns a node service. nodeLock is shared with the operations that change node state;
// monitoringEnabled issues monitor credentials for new nodes.
func New(
	hooks operation.Hooks,
	nodes NodeOperationRepository,
	nodeLock *operation.NodeStateLock,
	monitoringEnabled bool,
	credentials credentialIssuer,
	network networkLock,
	views nodeViewer,
) *Service {
	return &Service{
		hooks:             hooks,
		nodes:             nodes,
		nodeLock:          nodeLock,
		monitoringEnabled: monitoringEnabled,
		credentials:       credentials,
		network:           network,
		views:             views,
	}
}

// Reader builds node views from the nodes, their addresses, probes and host overrides.
type Reader struct {
	hooks      operation.Hooks
	nodes      NodeQueryRepository
	users      UserQueryRepository
	network    NetworkQueryRepository
	probes     ProbeQueryRepository
	hosts      hostLister
	dnsDomain  string
	dnsEnabled bool
}

// NewReader returns a node reader. dnsDomain and dnsEnabled describe the managed DNS zone.
func NewReader(
	hooks operation.Hooks,
	nodes NodeQueryRepository,
	users UserQueryRepository,
	network NetworkQueryRepository,
	probes ProbeQueryRepository,
	hosts hostLister,
	dnsDomain string,
	dnsEnabled bool,
) *Reader {
	return &Reader{
		hooks:      hooks,
		nodes:      nodes,
		users:      users,
		network:    network,
		probes:     probes,
		hosts:      hosts,
		dnsDomain:  dnsDomain,
		dnsEnabled: dnsEnabled,
	}
}

// NodeOperationRepository stores node operations.
type NodeOperationRepository interface {
	CreateNodeWithInbounds(context.Context, *panel.Node, []panel.Inbound) error
	DeleteNode(ctx context.Context, id int64) error
	GetNode(ctx context.Context, id int64) (*panel.Node, error)
	UpdateNode(ctx context.Context, n *panel.Node) error
}

// NodeQueryRepository reads nodes and their inbounds.
type NodeQueryRepository interface {
	GetNode(ctx context.Context, id int64) (*panel.Node, error)
	ListInbounds(ctx context.Context, nodeID int64) ([]panel.Inbound, error)
	ListNodes(ctx context.Context) ([]panel.Node, error)
}

// UserQueryRepository reads users.
type UserQueryRepository interface {
	GetUser(ctx context.Context, id int64) (*panel.User, error)
}

// NetworkQueryRepository reads node addresses and network operations.
type NetworkQueryRepository interface {
	ListAddresses(context.Context, int64) ([]panel.Address, error)
	ListNetworkOperations(context.Context, int64) ([]panel.NetworkOperation, error)
}

// ProbeQueryRepository reads probe results.
type ProbeQueryRepository interface {
	ListProbes(context.Context, int64) ([]panel.ProbeResult, error)
}
