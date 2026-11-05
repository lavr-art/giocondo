// Package nodestate owns the panel-side state of agent links: admission by certificate identity,
// reported node and installation state, desired configs and traffic records.
package nodestate

import (
	"context"
	"sync"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/nodeconfig"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/internal/service/singbox"
)

// NodeRepository reads nodes for config preparation and records their reported state.
type NodeRepository interface {
	nodeconfig.NodeSource
	SetNodeApplied(ctx context.Context, id int64, hash, applyErr string) error
	SetNodeDesired(ctx context.Context, id int64, hash string) error
	TouchNode(ctx context.Context, id int64, status panel.NodeStatus, seenAt time.Time) error
}

// TrafficRepository records agent traffic batches.
type TrafficRepository interface {
	RecordTrafficBatch(ctx context.Context, nodeID int64, instance string, seq uint64, deltas []panel.TrafficDelta) error
}

// InstallationRepository reads node installations and addresses and records installation state.
type InstallationRepository interface {
	ApplyInstallation(context.Context, int64, string, string) error
	InstallationByIdentity(context.Context, string) (*panel.Installation, error)
	ListAddresses(context.Context, int64) ([]panel.Address, error)
	TouchInstallation(context.Context, int64, panel.NodeStatus, time.Time) error
}

// UserRepository supplies the users of node configs.
type UserRepository = nodeconfig.UserSource

// Service records agent link state. One instance per panel: it caches the hashes already written
// to the database.
type Service struct {
	hooks   operation.Hooks // Now and Log
	nodes   NodeRepository
	users   UserRepository
	traffic TrafficRepository
	network InstallationRepository
	// allocator issues missing WireGuard peers while configs are prepared; it must be the one the
	// credential coordinator uses.
	allocator *nodeconfig.Allocator
	options   singbox.Options

	mu      sync.Mutex
	desired map[int64]string // what is already written to nodes.desired_hash
	applied map[int64]string // what is already written to nodes.applied_hash + apply_error
}

// New returns the agent link state service. allocator must be the one the credential coordinator
// uses; zero options select the singbox defaults.
func New(
	hooks operation.Hooks,
	nodes NodeRepository,
	users UserRepository,
	traffic TrafficRepository,
	network InstallationRepository,
	allocator *nodeconfig.Allocator,
	options singbox.Options,
) *Service {
	return &Service{
		hooks:     hooks,
		nodes:     nodes,
		users:     users,
		traffic:   traffic,
		network:   network,
		allocator: allocator,
		options:   options,
	}
}
