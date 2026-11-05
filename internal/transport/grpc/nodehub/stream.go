package nodehub

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/nodestate"
	"github.com/artemlavr/giocondo/internal/transport/grpc/pb"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// agentStream is the panel side of one admitted agent stream.
type agentStream struct {
	state  State
	stream pb.NodeService_SyncServer
	agent  *nodestate.Agent

	// agentHash is what runs on the node; failedHash is a config the node failed to apply:
	// it is not resent until it changes. sentHash is the config sent last and not answered yet.
	agentHash, failedHash, sentHash string
	runtimeBlocked                  bool
	// engineRuntime is set when Hello carried engine runtime state; health then reports it the same way.
	engineRuntime bool

	// throttleC is armed when a new config waits for the end of the interval since the previous send.
	applyEvery time.Duration
	lastSent   time.Time
	throttle   *time.Timer
	throttleC  <-chan time.Time
}

func (s *agentStream) touch(ctx context.Context, st panel.NodeStatus) {
	s.state.Touch(ctx, s.agent, st)
}

func (s *agentStream) applied(ctx context.Context, hash, applyErr string) {
	s.state.RecordApplied(ctx, s.agent, hash, applyErr)
}

// restore takes what the agent runs from Hello and records it.
func (s *agentStream) restore(ctx context.Context, hello *pb.Hello) {
	s.agentHash = hello.GetConfigHash()

	state := engineRuntimeState(hello.GetCapabilities(), hello.GetEngines())
	if state == nil {
		s.applied(ctx, s.agentHash, "")

		return
	}

	s.engineRuntime = true
	s.agentHash, s.failedHash = state.GetAppliedHash(), state.GetRejectedHash()
	s.runtimeBlocked = state.GetPhase() == nodesync.EnginePhaseBlocked
	s.applied(ctx, s.agentHash, state.GetError())
}

func (s *agentStream) stop() {
	if s.throttle != nil {
		s.throttle.Stop()
	}
}

// push sends the desired network state and, when it changed, the desired config.
func (s *agentStream) push(ctx context.Context) error {
	if err := s.checkRetired(ctx); err != nil {
		return err
	}

	// Send independent network desired state even when sing-box config is unchanged.
	if err := s.pushNetwork(ctx); err != nil {
		return err
	}

	return s.pushConfig(ctx)
}

func (s *agentStream) onThrottle(ctx context.Context) error {
	s.throttle, s.throttleC = nil, nil

	return s.push(ctx)
}

// checkRetired ends the stream of an installation that was retired while connected.
func (s *agentStream) checkRetired(ctx context.Context) error {
	err := s.state.CheckRetired(ctx, s.agent)
	if errors.Is(err, nodestate.ErrRetired) {
		return status.Error(codes.PermissionDenied, "installation retired")
	}

	return err
}

// pushNetwork sends the addresses of the installation in use once the node has an active installation.
func (s *agentStream) pushNetwork(ctx context.Context) error {
	n, ok, err := s.state.NetworkConfig(ctx, s.agent)
	if err != nil || !ok {
		return err
	}

	network := &pb.NetworkConfig{PrimaryIp: n.PrimaryIP, Aliases: n.Aliases, UdpPorts: n.UDPPorts}

	return s.stream.Send(&pb.PanelMessage{Msg: &pb.PanelMessage_NetworkConfig{NetworkConfig: network}})
}

// pushConfig sends a new config unless the node runs, rejected or is applying it; bursts are throttled.
func (s *agentStream) pushConfig(ctx context.Context) error {
	data, hash, err := s.state.DesiredConfig(ctx, s.agent.Node.ID)
	if err != nil {
		logger.FromCtx(ctx).Error("build config", "err", err)

		return nil // the node stays on its current config
	}

	if s.runtimeBlocked || hash == s.agentHash || hash == s.failedHash || hash == s.sentHash {
		return nil
	}

	if wait := s.applyEvery - time.Since(s.lastSent); !s.lastSent.IsZero() && wait > 0 {
		if s.throttleC == nil {
			s.throttle = time.NewTimer(wait)
			s.throttleC = s.throttle.C

			logger.FromCtx(ctx).Debug("config change throttled", "wait", wait.Round(time.Second))
		}

		return nil // rebuild when the timer fires
	}

	s.sentHash, s.lastSent = hash, time.Now()
	logger.FromCtx(ctx).Info("sending config", "hash", hash[:12])

	apply := &pb.ApplyConfig{ConfigJson: data, Hash: hash}

	return s.stream.Send(&pb.PanelMessage{Msg: &pb.PanelMessage_ApplyConfig{ApplyConfig: apply}})
}

func (s *agentStream) handle(ctx context.Context, hello *pb.Hello, m *pb.AgentMessage) error {
	switch msg := m.GetMsg().(type) {
	case *pb.AgentMessage_NetworkResult:
		s.onNetworkResult(ctx, msg.NetworkResult)
	case *pb.AgentMessage_ApplyResult:
		s.onApplyResult(ctx, msg.ApplyResult)
	case *pb.AgentMessage_Stats:
		return s.onStats(ctx, hello, msg.Stats)
	case *pb.AgentMessage_Health:
		s.onHealth(ctx, hello, msg.Health)
	}

	return nil
}

// onNetworkResult logs a failed network command. A command that a newer one replaced in the agent
// queue did not fail: the newer command gets its own result.
func (s *agentStream) onNetworkResult(ctx context.Context, r *pb.ApplyResult) {
	switch {
	case r.GetOk():
	case r.GetError() == nodesync.ErrNetworkConfigSuperseded.Error():
		logger.FromCtx(ctx).Debug("network configuration superseded on agent", "hash", short(r.GetHash()))
	default:
		logger.FromCtx(ctx).Error("agent UDP alias configuration failed", "err", r.GetError())
	}
}

func (s *agentStream) onApplyResult(ctx context.Context, r *pb.ApplyResult) {
	if r.GetHash() == s.sentHash {
		s.sentHash = ""
	}

	if r.GetOk() {
		s.agentHash, s.failedHash = r.GetHash(), ""

		s.applied(ctx, s.agentHash, "")
		logger.FromCtx(ctx).Info("config applied", "hash", short(r.GetHash()))

		return
	}

	// A newer config replaced this one in the agent queue: the node neither ran nor rejected it.
	if r.GetError() == nodesync.ErrConfigSuperseded.Error() {
		logger.FromCtx(ctx).Debug("config superseded on agent", "hash", short(r.GetHash()))

		return
	}

	s.failedHash = r.GetHash()

	errText := r.GetError()
	if errText == "" {
		errText = "apply failed"
	}

	s.applied(ctx, s.agentHash, errText)
	logger.FromCtx(ctx).Error("agent failed to apply config", "hash", short(r.GetHash()), "err", r.GetError())
}

// onStats acknowledges a batch only after it is saved; without an ack the agent resends it.
func (s *agentStream) onStats(ctx context.Context, hello *pb.Hello, stats *pb.Stats) error {
	traffic := make([]nodestate.UserTraffic, 0, len(stats.GetUsers()))
	for _, u := range stats.GetUsers() {
		traffic = append(traffic, nodestate.UserTraffic{Name: u.GetName(), Up: u.GetUp(), Down: u.GetDown()})
	}

	if err := s.state.RecordStats(ctx, s.agent, hello.GetInstanceId(), stats.GetSeq(), traffic); err != nil {
		logger.FromCtx(ctx).Error("save stats", "err", err)

		return nil
	}

	ack := &pb.StatsAck{Seq: stats.GetSeq()}

	return s.stream.Send(&pb.PanelMessage{Msg: &pb.PanelMessage_StatsAck{StatsAck: ack}})
}

func (s *agentStream) onHealth(ctx context.Context, hello *pb.Hello, hl *pb.Health) {
	s.touch(ctx, panel.NodeOnline)

	if state := engineRuntimeState(hello.GetCapabilities(), hl.GetEngines()); state != nil {
		s.agentHash, s.failedHash = state.GetAppliedHash(), state.GetRejectedHash()
		s.runtimeBlocked = state.GetPhase() == nodesync.EnginePhaseBlocked

		s.applied(ctx, s.agentHash, state.GetError())
	} else if !s.engineRuntime && hl.GetConfigHash() != "" && hl.GetConfigHash() != s.agentHash {
		s.agentHash = hl.GetConfigHash()

		s.applied(ctx, s.agentHash, "")
	}

	logger.FromCtx(ctx).Debug("health", "singbox_active", hl.GetSingboxActive(), "load1", hl.GetLoad1(),
		"mem_available", hl.GetMemAvailableBytes(), "agent_rss", hl.GetAgentRssBytes())

	if !hl.GetSingboxActive() {
		logger.FromCtx(ctx).Warn("sing-box is not running on node")
	}
}

// receive forwards stream messages until the stream fails or ctx ends.
func receive(ctx context.Context, stream pb.NodeService_SyncServer) (<-chan *pb.AgentMessage, <-chan error) {
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

	return recv, recvErr
}
