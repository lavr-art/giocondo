// Package nodehub is the panel side of the agent link: the Sync stream server.
// The panel gives the agent the desired sing-box config and records the reported traffic.
package nodehub

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/nodestate"
	"github.com/artemlavr/giocondo/internal/transport/grpc/nodeauth"
	"github.com/artemlavr/giocondo/internal/transport/grpc/pb"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// DefaultResync is how often the panel rebuilds the config of a connected node. This applies
// new users, disables, expired terms and limits without separate notifications.
const DefaultResync = 30 * time.Second

// DefaultApplyInterval allows at most one new config per node within this interval. Every apply
// restarts sing-box and drops client connections, so a burst of changes goes out as one config:
// the first one immediately, the rest within the interval as a single send at its end.
const DefaultApplyInterval = 30 * time.Second

// Hub implements pb.NodeServiceServer.
type Hub struct {
	pb.UnimplementedNodeServiceServer

	State  State
	Resync time.Duration // 0 — DefaultResync
	// ApplyInterval is the minimum gap between config sends to a node; 0 means DefaultApplyInterval.
	ApplyInterval time.Duration
	// NodeName extracts the node name from the stream context; nil means nodeauth.NodeName (certificate CN).
	NodeName func(ctx context.Context) (string, error)

	mu     sync.Mutex
	notify map[int64][]chan struct{}
}

// Kick asks all streams to rebuild the config immediately instead of waiting for Resync.
// The panel calls it after users or protocols change.
func (h *Hub) Kick() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, chans := range h.notify {
		for _, c := range chans {
			select {
			case c <- struct{}{}:
			default:
			}
		}
	}
}

// Sync serves the stream of one agent.
func (h *Hub) Sync(stream pb.NodeService_SyncServer) error {
	ctx := stream.Context()

	agent, err := h.admit(ctx)
	if err != nil {
		return err
	}

	ctx = logger.With(ctx, "node", agent.Identity)

	first, err := stream.Recv()
	if err != nil {
		return err
	}

	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "first message must be hello")
	}

	logger.FromCtx(ctx).Info("agent connected", "agent", hello.GetAgentVersion(), "sing_box", hello.GetSingboxVersion())

	applyEvery := h.ApplyInterval
	if applyEvery <= 0 {
		applyEvery = DefaultApplyInterval
	}

	s := &agentStream{state: h.State, stream: stream, agent: agent, applyEvery: applyEvery}

	s.touch(ctx, panel.NodeOnline)
	defer s.touch(ctx, panel.NodeOffline)

	kick, unsubscribe := h.subscribe(agent.Node.ID)
	defer unsubscribe()

	s.restore(ctx, hello)
	defer s.stop()

	if err := s.push(ctx); err != nil {
		return err
	}

	recv, recvErr := receive(ctx, stream)

	resync := h.Resync
	if resync <= 0 {
		resync = DefaultResync
	}

	t := time.NewTicker(resync)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-recvErr:
			logger.FromCtx(ctx).Info("agent disconnected", "err", err)

			return nil
		case <-t.C:
			err = s.push(ctx)
		case <-kick:
			err = s.push(ctx)
		case <-s.throttleC:
			err = s.onThrottle(ctx)
		case m := <-recv:
			err = s.handle(ctx, hello, m)
		}

		if err != nil {
			return err
		}
	}
}

// SubscribeReconciliation subscribes to changes requiring desired configs to be rebuilt.
func (h *Hub) SubscribeReconciliation() (<-chan struct{}, func()) {
	return h.subscribe(reconcileKey)
}

// reconcileKey is the Kick subscription for reconciliation (node ids start at 1).
const reconcileKey = -1

// ReconcileOnce rebuilds desired configurations of enabled nodes, including offline nodes.
func (h *Hub) ReconcileOnce(ctx context.Context) {
	h.State.ReconcileOnce(ctx)
}

// admit identifies the agent by its certificate and refuses unknown, disabled and retired installations.
func (h *Hub) admit(ctx context.Context) (*nodestate.Agent, error) {
	nameOf := h.NodeName
	if nameOf == nil {
		nameOf = nodeauth.NodeName
	}

	name, err := nameOf(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}

	agent, err := h.State.Admit(ctx, name)

	switch {
	case err == nil:
		return agent, nil
	case errors.Is(err, nodestate.ErrRetired):
		return nil, status.Error(codes.PermissionDenied, "installation retired")
	case errors.Is(err, nodestate.ErrNotRegistered):
		return nil, status.Errorf(codes.PermissionDenied, "node %q is not registered", name)
	case errors.Is(err, nodestate.ErrDisabled):
		return nil, status.Errorf(codes.PermissionDenied, "node %q is disabled", name)
	default:
		return nil, status.Error(codes.Internal, "store error")
	}
}

func (h *Hub) subscribe(nodeID int64) (chan struct{}, func()) {
	c := make(chan struct{}, 1)

	h.mu.Lock()
	if h.notify == nil {
		h.notify = map[int64][]chan struct{}{}
	}

	h.notify[nodeID] = append(h.notify[nodeID], c)
	h.mu.Unlock()

	return c, func() {
		h.mu.Lock()
		defer h.mu.Unlock()

		list := h.notify[nodeID]
		for i, x := range list {
			if x == c {
				h.notify[nodeID] = append(list[:i], list[i+1:]...)

				break
			}
		}
	}
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}

	return h
}

// Legacy peers have no capabilities; their existing hash semantics stay unchanged.
func engineRuntimeState(capabilities []string, states []*pb.EngineState) *pb.EngineState {
	if !slices.Contains(capabilities, nodesync.EngineRuntimeCapability) {
		return nil
	}

	for _, state := range states {
		if state.GetId() == nodesync.SingBoxEngineID {
			return state
		}
	}

	return nil
}
