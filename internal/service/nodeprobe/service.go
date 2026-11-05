// Package nodeprobe checks the protocols and addresses of a node with the configured checkers.
package nodeprobe

import (
	"context"
	"sync"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// monitorIssuer issues the service monitor credentials of a node.
type monitorIssuer interface {
	EnsureMonitor(ctx context.Context, nodeID int64) (*protocol.User, error)
}

// Service checks node protocols from the panel and from the external probe points.
type Service struct {
	nodes       NodeRepository
	checker     probe.Checker
	probePoints map[string]probe.Checker
	monitor     monitorIssuer
	// A WireGuard peer has one roaming endpoint: overlapping probes with the same monitor key would
	// move it between checking points and produce false failures.
	wgProbeMu sync.Mutex
}

// New returns a node probe service. checker checks from the panel; probePoints are the external check
// points by name.
func New(
	nodes NodeRepository,
	checker probe.Checker,
	probePoints map[string]probe.Checker,
	monitor monitorIssuer,
) *Service {
	return &Service{nodes: nodes, checker: checker, probePoints: probePoints, monitor: monitor}
}

// NodeRepository supplies the inbounds a node probe checks.
type NodeRepository interface {
	ListInbounds(ctx context.Context, nodeID int64) ([]panel.Inbound, error)
}
