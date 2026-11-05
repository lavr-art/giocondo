// Package network orchestrates node network changes.
package network

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// nodeQueries reads node views and the DNS records they need.
type nodeQueries interface {
	GetNode(ctx context.Context, id int64) (panel.NodeView, error)
	PreviewDNS(ctx context.Context, id int64) (panel.DNSPreview, error)
}

// hostLister lists the host overrides of a node.
type hostLister interface {
	ListHosts(ctx context.Context, nodeID int64) ([]panel.HostView, error)
}

// addressChecker checks that every node protocol works on an address.
type addressChecker interface {
	CheckAddress(ctx context.Context, n panel.Node, ip string) error
}

// Service orchestrates managed DNS, node addresses and IP rotation.
type Service struct {
	hooks        operation.Hooks
	nodes        NodeRepository
	network      NetworkOperationRepository
	dns          DNSManager
	probeEnabled bool
	resolveA     func(ctx context.Context, host, ip string) error
	executor     operation.TaskExecutor
	drainDelay   time.Duration
	nodeLock     *operation.NodeStateLock
	installer    candidateInstaller
	queries      nodeQueries
	hosts        hostLister
	addresses    addressChecker
}

// New returns a network service. dns may be nil when managed DNS is disabled; probeEnabled checks a
// new address before traffic moves to it; resolveA verifies that a host resolves to an address.
// nodeLock is shared with the operations that change node state.
func New(
	hooks operation.Hooks,
	nodes NodeRepository,
	network NetworkOperationRepository,
	dns DNSManager,
	probeEnabled bool,
	resolveA func(ctx context.Context, host, ip string) error,
	executor operation.TaskExecutor,
	drainDelay time.Duration,
	nodeLock *operation.NodeStateLock,
	installer candidateInstaller,
	queries nodeQueries,
	hosts hostLister,
	addresses addressChecker,
) *Service {
	return &Service{
		hooks:        hooks,
		nodes:        nodes,
		network:      network,
		dns:          dns,
		probeEnabled: probeEnabled,
		resolveA:     resolveA,
		executor:     executor,
		drainDelay:   drainDelay,
		nodeLock:     nodeLock,
		installer:    installer,
		queries:      queries,
		hosts:        hosts,
		addresses:    addresses,
	}
}

// NetworkOperationRepository stores node addresses, installations and network operations.
type NetworkOperationRepository interface {
	ActivateInstallation(context.Context, int64, int64, string) error
	CreateAddress(context.Context, *panel.Address) error
	CreateInstallation(context.Context, *panel.Installation) error
	CreateNetworkOperation(context.Context, *panel.NetworkOperation) error
	DeleteAddress(context.Context, int64, int64) error
	GetNetworkOperation(context.Context, string) (*panel.NetworkOperation, error)
	InstallationByIdentity(context.Context, string) (*panel.Installation, error)
	ListAddresses(context.Context, int64) ([]panel.Address, error)
	ListNetworkOperations(context.Context, int64) ([]panel.NetworkOperation, error)
	NetworkLocked(context.Context, int64) (bool, error)
	SaveNetworkOperation(context.Context, *panel.NetworkOperation) error
	SetInstallationState(context.Context, int64, panel.InstallationState) error
	SetManagedDNS(context.Context, int64, string, string) error
}

// NodeRepository reads nodes and updates their inbounds.
type NodeRepository interface {
	GetNode(ctx context.Context, id int64) (*panel.Node, error)
	ListInbounds(ctx context.Context, nodeID int64) ([]panel.Inbound, error)
	UpdateInbound(ctx context.Context, in *panel.Inbound) error
}

// DNSManager manages DNS records for nodes.
type DNSManager interface {
	Ensure(context.Context, string, string, string) (panel.DNSRecord, error)
	Lookup(context.Context, string) ([]panel.DNSRecord, error)
	Delete(context.Context, string) error
}

// candidateInstaller installs, starts and stops node installations over SSH.
type candidateInstaller interface {
	InstallCandidate(
		context.Context,
		*panel.Node,
		panel.Installation,
		panel.SSHInput,
		*panel.SSHInput,
		string,
	) error
	StartServices(context.Context, panel.SSHInput) error
	StopServices(context.Context, panel.SSHInput) error
}
