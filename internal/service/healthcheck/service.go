// Package healthcheck probes nodes and records results.
package healthcheck

import (
	"context"
	"sync"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// monitorIssuer issues the service monitor credentials of a node.
type monitorIssuer interface {
	EnsureMonitor(ctx context.Context, nodeID int64) (*protocol.User, error)
}

// protocolProber runs one protocol check, serializing the checks that cannot overlap.
type protocolProber interface {
	CheckProtocol(ctx context.Context, c probe.Checker, r probe.Request) error
}

// nodeViewer builds the API view of a node.
type nodeViewer interface {
	GetNode(ctx context.Context, id int64) (panel.NodeView, error)
}

// Service probes the node addresses and records the results and node up/down events.
type Service struct {
	hooks          operation.Hooks
	nodes          NodeRepository
	network        AddressRepository
	probes         ProbeRepository
	checker        probe.Checker
	probePoints    map[string]probe.Checker
	runProbes      func(ctx context.Context, probes []func(context.Context)) error
	healthInterval time.Duration
	nodeLock       *operation.NodeStateLock
	monitor        monitorIssuer
	prober         protocolProber
	views          nodeViewer
	healthMu       sync.Mutex
}

// New returns a health check service. checker probes from the panel and probePoints from the external
// check points; runProbes runs the probes of one pass with bounded concurrency. A zero healthInterval
// selects DefaultHealthInterval. nodeLock is shared with the operations that change node state.
func New(
	hooks operation.Hooks,
	nodes NodeRepository,
	network AddressRepository,
	probes ProbeRepository,
	checker probe.Checker,
	probePoints map[string]probe.Checker,
	runProbes func(ctx context.Context, probes []func(context.Context)) error,
	healthInterval time.Duration,
	nodeLock *operation.NodeStateLock,
	monitor monitorIssuer,
	prober protocolProber,
	views nodeViewer,
) *Service {
	return &Service{
		hooks:          hooks,
		nodes:          nodes,
		network:        network,
		probes:         probes,
		checker:        checker,
		probePoints:    probePoints,
		runProbes:      runProbes,
		healthInterval: healthInterval,
		nodeLock:       nodeLock,
		monitor:        monitor,
		prober:         prober,
		views:          views,
	}
}

// NodeRepository reads the checked nodes and stores their down notification flag.
type NodeRepository interface {
	GetNode(ctx context.Context, id int64) (*panel.Node, error)
	ListInbounds(ctx context.Context, nodeID int64) ([]panel.Inbound, error)
	ListNodes(ctx context.Context) ([]panel.Node, error)
	SetNodeDownNotified(ctx context.Context, id int64, v bool) error
}

// AddressRepository reads the node addresses to check.
type AddressRepository interface {
	ListAddresses(context.Context, int64) ([]panel.Address, error)
}

// ProbeRepository stores health check results.
type ProbeRepository interface {
	ListProbes(context.Context, int64) ([]panel.ProbeResult, error)
	RecordProbe(context.Context, panel.ProbeResult) (panel.ProbeResult, error)
}
