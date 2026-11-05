package nodehub

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/nodestate"
)

// State admits agents, records their reported state and prepares their desired configs.
type State interface {
	Admit(ctx context.Context, identity string) (*nodestate.Agent, error)
	Touch(ctx context.Context, a *nodestate.Agent, st panel.NodeStatus)
	RecordApplied(ctx context.Context, a *nodestate.Agent, hash, applyErr string)
	CheckRetired(ctx context.Context, a *nodestate.Agent) error
	NetworkConfig(ctx context.Context, a *nodestate.Agent) (nodestate.Network, bool, error)
	DesiredConfig(ctx context.Context, nodeID int64) ([]byte, string, error)
	RecordStats(
		ctx context.Context, a *nodestate.Agent, instance string, seq uint64, traffic []nodestate.UserTraffic,
	) error
	ReconcileOnce(ctx context.Context)
}
