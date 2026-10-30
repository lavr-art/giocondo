// Package nodehub is the panel side of the agent link: the Sync stream server.
// The panel gives the agent the desired sing-box config and records the reported traffic.
package nodehub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/artemlavr/giocondo/internal/agent"
	"github.com/artemlavr/giocondo/internal/nodeapi/pb"
	"github.com/artemlavr/giocondo/internal/nodeapi/pki"
	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
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

	Store   store.Store
	Options singbox.Options
	Resync  time.Duration // 0 — DefaultResync
	// ApplyInterval is the minimum gap between config sends to a node; 0 means DefaultApplyInterval.
	ApplyInterval time.Duration
	Now           func() time.Time // nil — time.Now
	Log           *slog.Logger     // nil — slog.Default()
	// NodeName extracts the node name from the stream context; nil means pki.NodeName (certificate CN).
	NodeName func(ctx context.Context) (string, error)

	mu       sync.Mutex
	lastSeqs map[string]seqMark // node -> last recorded statistics seq
	notify   map[int64][]chan struct{}
	desired  map[int64]string // what is already written to nodes.desired_hash
	applied  map[int64]string // what is already written to nodes.applied_hash + apply_error
}

type seqMark struct {
	instance string
	seq      uint64
}

func (h *Hub) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Hub) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
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

func (h *Hub) findNode(ctx context.Context, name string) (*store.Node, error) {
	nodes, err := h.Store.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		if n.Name == name {
			return &n, nil
		}
	}
	return nil, store.ErrNotFound
}

// Sync serves the stream of one agent.
func (h *Hub) Sync(stream pb.NodeService_SyncServer) error {
	ctx := stream.Context()
	nameOf := h.NodeName
	if nameOf == nil {
		nameOf = pki.NodeName
	}
	name, err := nameOf(ctx)
	if err != nil {
		return status.Error(codes.Unauthenticated, err.Error())
	}
	installation, installErr := h.Store.InstallationByIdentity(ctx, name)
	var node *store.Node
	if installErr == nil {
		node, err = h.Store.GetNode(ctx, installation.NodeID)
	} else if errors.Is(installErr, store.ErrNotFound) {
		node, err = h.findNode(ctx, name)
	} else {
		err = installErr
	}
	if installErr == nil && installation.State == "retired" {
		return status.Error(codes.PermissionDenied, "installation retired")
	}
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Errorf(codes.PermissionDenied, "node %q is not registered", name)
	case err != nil:
		return status.Error(codes.Internal, "store error")
	case node.Status == store.NodeDisabled:
		return status.Errorf(codes.PermissionDenied, "node %q is disabled", name)
	}
	touch := func(st store.NodeStatus) {
		if installation.ID == 0 {
			if i, e := h.Store.InstallationByIdentity(ctx, name); e == nil {
				installation = i
			} else {
				h.touch(node.ID, st)
				return
			}
		}
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.Store.TouchInstallation(c, installation.ID, st, h.now()); err != nil {
			h.log().Error("touch installation", "err", err)
		}
	}
	applied := func(hash, applyErr string) {
		if installation.ID == 0 {
			if i, e := h.Store.InstallationByIdentity(ctx, name); e == nil {
				installation = i
			} else {
				h.setApplied(node.ID, hash, applyErr)
				return
			}
		}
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.Store.ApplyInstallation(c, installation.ID, hash, applyErr); err != nil {
			h.log().Error("apply installation", "err", err)
		}
	}
	log := h.log().With("node", name)

	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "first message must be hello")
	}
	log.Info("agent connected", "agent", hello.GetAgentVersion(), "sing_box", hello.GetSingboxVersion())
	touch(store.NodeOnline)
	defer touch(store.NodeOffline)

	kick, unsubscribe := h.subscribe(node.ID)
	defer unsubscribe()

	applyEvery := h.ApplyInterval
	if applyEvery <= 0 {
		applyEvery = DefaultApplyInterval
	}
	// agentHash is what runs on the node; failedHash is a config the node failed to apply:
	// it is not resent until it changes. throttleC is armed when a new config waits for
	// the end of the interval since the previous send.
	agentHash, failedHash, sentHash := hello.GetConfigHash(), "", ""
	runtimeState := engineRuntimeState(hello.GetCapabilities(), hello.GetEngines())
	runtimeBlocked := false
	if runtimeState != nil {
		agentHash, failedHash = runtimeState.GetAppliedHash(), runtimeState.GetRejectedHash()
		runtimeBlocked = runtimeState.GetPhase() == "blocked"
		applied(agentHash, runtimeState.GetError())
	} else {
		applied(agentHash, "")
	}
	var lastSent time.Time
	var throttle *time.Timer
	var throttleC <-chan time.Time
	defer func() {
		if throttle != nil {
			throttle.Stop()
		}
	}()
	push := func() error {
		if installation.ID != 0 {
			i, e := h.Store.InstallationByIdentity(ctx, name)
			if e != nil {
				return e
			}
			if i.State == "retired" {
				return status.Error(codes.PermissionDenied, "installation retired")
			}
		}

		// Send independent network desired state even when sing-box config is unchanged.
		current, e := h.Store.GetNode(ctx, node.ID)
		if e != nil {
			return e
		}
		if current.ActiveInstallation != 0 {
			inst := installation
			if inst.ID == 0 {
				inst, e = h.Store.InstallationByIdentity(ctx, name)
				if e != nil {
					return e
				}
			}
			addresses, e := h.Store.ListAddresses(ctx, node.ID)
			if e != nil {
				return e
			}
			network := &pb.NetworkConfig{PrimaryIp: inst.IP}
			for _, a := range addresses {
				if a.InstallationID == inst.ID {
					network.Aliases = append(network.Aliases, a.IP)
				}
			}
			inbounds, e := h.Store.ListInbounds(ctx, node.ID)
			if e != nil {
				return e
			}
			for _, in := range inbounds {
				if in.Spec.Type != singbox.TypeVLESSReality {
					network.UdpPorts = append(network.UdpPorts, uint32(in.Spec.Port))
				}
			}
			if network.PrimaryIp != "" {
				if e = stream.Send(&pb.PanelMessage{Msg: &pb.PanelMessage_NetworkConfig{NetworkConfig: network}}); e != nil {
					return e
				}
			}
		}
		data, hash, err := h.buildConfig(ctx, node.ID)
		if err != nil {
			log.Error("build config", "err", err)
			return nil // the node stays on its current config
		}
		if runtimeBlocked || hash == agentHash || hash == failedHash || hash == sentHash {
			return nil
		}
		if wait := applyEvery - time.Since(lastSent); !lastSent.IsZero() && wait > 0 {
			if throttleC == nil {
				throttle = time.NewTimer(wait)
				throttleC = throttle.C
				log.Debug("config change throttled", "wait", wait.Round(time.Second))
			}
			return nil // rebuild when the timer fires
		}
		sentHash, lastSent = hash, time.Now()
		log.Info("sending config", "hash", hash[:12])
		return stream.Send(&pb.PanelMessage{Msg: &pb.PanelMessage_ApplyConfig{ApplyConfig: &pb.ApplyConfig{ConfigJson: data, Hash: hash}}})
	}
	if err := push(); err != nil {
		return err
	}

	recv := make(chan *pb.AgentMessage)
	recvErr := make(chan error, 1)
	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			select {
			case recv <- m:
			case <-ctx.Done():
				return
			}
		}
	}()

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
			log.Info("agent disconnected", "err", err)
			return nil
		case <-t.C:
			if err := push(); err != nil {
				return err
			}
		case <-kick:
			if err := push(); err != nil {
				return err
			}
		case <-throttleC:
			throttle, throttleC = nil, nil
			if err := push(); err != nil {
				return err
			}
		case m := <-recv:
			switch msg := m.GetMsg().(type) {
			case *pb.AgentMessage_NetworkResult:
				if !msg.NetworkResult.Ok {
					log.Error("agent UDP alias configuration failed", "err", msg.NetworkResult.Error)
				}
			case *pb.AgentMessage_ApplyResult:
				r := msg.ApplyResult
				if r.GetHash() == sentHash {
					sentHash = ""
				}
				if r.GetOk() {
					agentHash, failedHash = r.GetHash(), ""
					applied(agentHash, "")
					log.Info("config applied", "hash", short(r.GetHash()))
				} else {
					failedHash = r.GetHash()
					errText := r.GetError()
					if errText == "" {
						errText = "apply failed"
					}
					applied(agentHash, errText)
					log.Error("agent failed to apply config", "hash", short(r.GetHash()), "err", r.GetError())
				}
			case *pb.AgentMessage_Stats:
				if err := h.saveStats(ctx, node.ID, name, hello.GetInstanceId(), msg.Stats); err != nil {
					log.Error("save stats", "err", err)
					continue // without an ack the agent resends the batch
				}
				if err := stream.Send(&pb.PanelMessage{Msg: &pb.PanelMessage_StatsAck{StatsAck: &pb.StatsAck{Seq: msg.Stats.GetSeq()}}}); err != nil {
					return err
				}
			case *pb.AgentMessage_Health:
				hl := msg.Health
				touch(store.NodeOnline)
				if state := engineRuntimeState(hello.GetCapabilities(), hl.GetEngines()); state != nil {
					agentHash, failedHash = state.GetAppliedHash(), state.GetRejectedHash()
					runtimeBlocked = state.GetPhase() == "blocked"
					applied(agentHash, state.GetError())
				} else if runtimeState == nil && hl.GetConfigHash() != "" && hl.GetConfigHash() != agentHash {
					agentHash = hl.GetConfigHash()
					applied(agentHash, "")
				}
				log.Debug("health", "singbox_active", hl.GetSingboxActive(), "load1", hl.GetLoad1(),
					"mem_available", hl.GetMemAvailableBytes(), "agent_rss", hl.GetAgentRssBytes())
				if !hl.GetSingboxActive() {
					log.Warn("sing-box is not running on node")
				}
			}
		}
	}
}

func (h *Hub) touch(nodeID int64, st store.NodeStatus) {
	// A separate context: the stream context is already cancelled when the stream closes.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Store.TouchNode(ctx, nodeID, st, h.now()); err != nil {
		h.log().Error("touch node", "node_id", nodeID, "err", err)
	}
}

// buildConfig builds the node config from the database and returns it with its hash.
func (h *Hub) buildConfig(ctx context.Context, nodeID int64) ([]byte, string, error) {
	node, users, err := store.NodeConfigInput(ctx, h.Store, nodeID, h.now())
	if err != nil {
		return nil, "", err
	}
	if wg := node.WireGuardInbound(); wg != nil && wg.WireGuard != nil {
		if err := EnsureWGPeers(ctx, h.Store, wg.WireGuard.WGSubnet()); err != nil {
			return nil, "", fmt.Errorf("wg peers: %w", err)
		}
		if node, users, err = store.NodeConfigInput(ctx, h.Store, nodeID, h.now()); err != nil {
			return nil, "", err
		}
	}
	cfg, err := singbox.Build(node, users, h.Options)
	if err != nil {
		return nil, "", err
	}
	data, err := cfg.Marshal()
	if err != nil {
		return nil, "", err
	}
	hash := agent.Hash(data)
	h.setDesired(nodeID, hash)
	return data, hash, nil
}

// setDesired writes the built config hash to nodes.desired_hash if it changed.
func (h *Hub) setDesired(nodeID int64, hash string) {
	h.mu.Lock()
	same := h.desired[nodeID] == hash
	h.mu.Unlock()
	if same {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Store.SetNodeDesired(ctx, nodeID, hash); err != nil {
		h.log().Error("save desired config hash", "node_id", nodeID, "err", err)
		return
	}
	h.mu.Lock()
	if h.desired == nil {
		h.desired = map[int64]string{}
	}
	h.desired[nodeID] = hash
	h.mu.Unlock()
}

// setApplied records which config runs on the node and how the last apply ended.
func (h *Hub) setApplied(nodeID int64, hash, applyErr string) {
	key := hash + "\x00" + applyErr
	h.mu.Lock()
	same := h.applied[nodeID] == key
	h.mu.Unlock()
	if same {
		return
	}
	// A separate context: it is also called while the stream is closing.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Store.SetNodeApplied(ctx, nodeID, hash, applyErr); err != nil {
		h.log().Error("save applied config hash", "node_id", nodeID, "err", err)
		return
	}
	h.mu.Lock()
	if h.applied == nil {
		h.applied = map[int64]string{}
	}
	h.applied[nodeID] = key
	h.mu.Unlock()
}

// Reconcile rebuilds the configs of all enabled nodes, offline ones included, until ctx is cancelled,
// every interval and after Kick. This keeps nodes.desired_hash current and the panel shows
// which nodes wait for a new config (dirty) even when their agent is not connected.
func (h *Hub) Reconcile(ctx context.Context, interval time.Duration) {
	kick, unsubscribe := h.subscribe(reconcileKey)
	defer unsubscribe()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		h.reconcileOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-kick:
		}
	}
}

// reconcileKey is the Kick subscription of Reconcile (node ids start at 1).
const reconcileKey = -1

func (h *Hub) reconcileOnce(ctx context.Context) {
	nodes, err := h.Store.ListNodes(ctx)
	if err != nil {
		if ctx.Err() == nil {
			h.log().Error("reconcile: list nodes", "err", err)
		}
		return
	}
	for _, n := range nodes {
		if n.Status == store.NodeDisabled {
			continue
		}
		// The cache is checked against the database: a deleted node's id may be reused by a new one.
		h.mu.Lock()
		if h.desired == nil {
			h.desired = map[int64]string{}
		}
		h.desired[n.ID] = n.DesiredHash
		h.mu.Unlock()
		if _, _, err := h.buildConfig(ctx, n.ID); err != nil && ctx.Err() == nil {
			h.log().Error("reconcile: build config", "node", n.Name, "err", err)
		}
	}
}

// saveStats records traffic. A repeated batch (same instance and seq not above the recorded one)
// is not recorded but is acknowledged.
func (h *Hub) saveStats(ctx context.Context, nodeID int64, node, instance string, s *pb.Stats) error {
	h.mu.Lock()
	last, ok := h.lastSeqs[node]
	h.mu.Unlock()
	if ok && instance != "" && last.instance == instance && s.GetSeq() <= last.seq {
		return nil
	}
	users, err := h.Store.ListUsers(ctx)
	if err != nil {
		return err
	}
	ids := make(map[string]int64, len(users))
	for _, u := range users {
		ids[u.Name] = u.ID
	}
	at := h.now()
	var deltas []store.TrafficDelta
	for _, ut := range s.GetUsers() {
		id, ok := ids[ut.GetName()]
		if !ok {
			h.log().Warn("traffic for unknown user", "node", node, "user", ut.GetName())
			continue
		}
		if ut.GetUp() < 0 || ut.GetDown() < 0 {
			continue
		}
		deltas = append(deltas, store.TrafficDelta{UserID: id, NodeID: nodeID, At: at, Up: ut.GetUp(), Down: ut.GetDown()})
	}
	if err := h.Store.AddTraffic(ctx, deltas); err != nil {
		return err
	}
	h.mu.Lock()
	if h.lastSeqs == nil {
		h.lastSeqs = map[string]seqMark{}
	}
	h.lastSeqs[node] = seqMark{instance: instance, seq: s.GetSeq()}
	h.mu.Unlock()
	return nil
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// Legacy peers have no capabilities; their existing hash semantics stay unchanged.
func engineRuntimeState(capabilities []string, states []*pb.EngineState) *pb.EngineState {
	supported := false
	for _, capability := range capabilities {
		if capability == "engine-runtime-v1" {
			supported = true
			break
		}
	}
	if !supported {
		return nil
	}
	for _, state := range states {
		if state.GetId() == "sing-box" {
			return state
		}
	}
	return nil
}
