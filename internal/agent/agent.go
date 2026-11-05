package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"google.golang.org/protobuf/proto"
	"log/slog"
	"time"

	"github.com/artemlavr/giocondo/internal/agent/engine"
	"github.com/artemlavr/giocondo/internal/nodeapi/pb"
)

// Panel is one of the panel addresses.
type Panel struct {
	Addr   string // for logs
	Client pb.NodeServiceClient
}

// Agent holds the Sync stream with the panel and executes its commands.
type Agent struct {
	NetworkApplier func(context.Context, *pb.NetworkConfig) error
	Panels         []Panel // primary address and fallbacks, in order of preference
	Engines        *engine.Manager
	Version        string
	StatsInterval  time.Duration
	HealthInterval time.Duration
	Log            *slog.Logger

	acc            Accumulator
	instanceID     string
	started        time.Time
	networkJobs    chan *pb.NetworkConfig
	networkResults chan *pb.ApplyResult
	procDir        string // "/proc"; replaced in tests
}

const (
	backoffFirst = time.Second
	backoffLimit = time.Minute
)

func (a *Agent) init() {
	if a.Log == nil {
		a.Log = slog.Default()
	}
	if a.StatsInterval <= 0 {
		a.StatsInterval = time.Minute
	}
	if a.HealthInterval <= 0 {
		a.HealthInterval = time.Minute
	}
	if a.procDir == "" {
		a.procDir = "/proc"
	}
	if a.instanceID == "" {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		a.instanceID = hex.EncodeToString(b)
	}
	a.started = time.Now()
}

// Run works until ctx is cancelled: connects to the panel and reconnects after drops.
// The address is sticky: after a long session drops the agent returns to the same address, after a failure
// it tries the next one at once; the backoff pause happens only when all addresses failed in a row.
func (a *Agent) Run(ctx context.Context) error {
	if len(a.Panels) == 0 {
		return errors.New("no panel addresses")
	}
	a.init()
	if a.Engines == nil || a.Engines.Runtime("sing-box") == nil {
		return errors.New("sing-box runtime is required")
	}
	for _, state := range a.Engines.States() {
		r := a.Engines.Runtime(state.ID)
		r.OnTraffic = a.acc.Add
		if err := r.Open(ctx); err != nil {
			a.Log.Error("engine recovery blocked", "engine", state.ID, "err", err)
		}
	}
	a.Engines.Start(ctx)
	a.Engines.Monitor(ctx, a.StatsInterval, a.HealthInterval)
	defer a.Engines.Wait()
	a.networkJobs = make(chan *pb.NetworkConfig, 1)
	a.networkResults = make(chan *pb.ApplyResult, 1)
	go a.networkLoop(ctx)
	backoff := backoffFirst
	cur, failed := 0, 0 // failed counts consecutive failures since the last long session
	for {
		p := a.Panels[cur]
		start := time.Now()
		err := a.session(ctx, p)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Since(start) > 30*time.Second {
			// The session lived long, so it was a drop, not a failure: use the same address again.
			backoff, failed = backoffFirst, 0
		} else {
			failed++
			cur = (cur + 1) % len(a.Panels)
			if failed%len(a.Panels) != 0 {
				a.Log.Warn("panel unavailable, trying next address", "addr", p.Addr, "err", err, "next", a.Panels[cur].Addr)
				continue
			}
		}
		a.Log.Warn("panel session ended", "addr", p.Addr, "err", err, "retry_in", backoff)
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		if failed > 0 {
			backoff = min(backoff*2, backoffLimit)
		}
	}
}

func (a *Agent) session(ctx context.Context, p Panel) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := p.Client.Sync(sctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	send := func(m *pb.AgentMessage) error { return stream.Send(m) }

	states := a.engineStates()
	legacy := legacyEngineState(states)
	hello := &pb.Hello{
		AgentVersion: a.Version, SingboxVersion: legacy.Version, ConfigHash: legacy.AppliedHash,
		InstanceId: a.instanceID, Capabilities: []string{"engine-runtime-v1"}, Engines: states,
	}
	if err := send(&pb.AgentMessage{Msg: &pb.AgentMessage_Hello{Hello: hello}}); err != nil {
		return fmt.Errorf("send hello: %w", err)
	}
	a.Log.Info("connected to panel", "addr", p.Addr, "config_hash", short(hello.ConfigHash))

	recv := make(chan *pb.PanelMessage)
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
			case <-sctx.Done():
				return
			}
		}
	}()

	statsT := time.NewTicker(a.StatsInterval)
	defer statsT.Stop()
	healthT := time.NewTicker(a.HealthInterval)
	defer healthT.Stop()
	if err := send(a.health(ctx)); err != nil {
		return err
	}
	// A batch left unacknowledged in the previous session is sent right away.
	if s := a.acc.Next(); s != nil {
		if err := send(&pb.AgentMessage{Msg: &pb.AgentMessage_Stats{Stats: s}}); err != nil {
			return err
		}
	}

	pending := map[string]*engine.Future{}
	resultsT := time.NewTicker(50 * time.Millisecond)
	resultsT.Stop()
	var resultsC <-chan time.Time
	defer resultsT.Stop()
	flush := func() error {
		for hash, f := range pending {
			select {
			case <-f.Done():
				res := &pb.ApplyResult{Hash: hash, Ok: f.Err() == nil}
				if e := f.Err(); e != nil {
					res.Error = e.Error()
				}
				delete(pending, hash)
				if e := send(&pb.AgentMessage{Msg: &pb.AgentMessage_ApplyResult{ApplyResult: res}}); e != nil {
					return e
				}
				if e := send(a.health(ctx)); e != nil {
					return e
				}
			default:
			}
		}
		if len(pending) == 0 {
			resultsT.Stop()
			resultsC = nil
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			_ = stream.CloseSend()
			return ctx.Err()
		case err := <-recvErr:
			return err
		case m := <-recv:
			switch msg := m.GetMsg().(type) {
			case *pb.PanelMessage_ApplyConfig:
				cfg := msg.ApplyConfig
				if Hash(cfg.GetConfigJson()) != cfg.GetHash() {
					if e := send(&pb.AgentMessage{Msg: &pb.AgentMessage_ApplyResult{ApplyResult: &pb.ApplyResult{Hash: cfg.GetHash(), Error: "config hash mismatch"}}}); e != nil {
						return e
					}
				} else {
					pending[cfg.GetHash()] = a.Engines.Submit("sing-box", cfg.GetConfigJson(), cfg.GetHash())
					if resultsC == nil {
						resultsT.Reset(50 * time.Millisecond)
						resultsC = resultsT.C
					}
				}
			case *pb.PanelMessage_NetworkConfig:
				select {
				case a.networkJobs <- msg.NetworkConfig:
				default:
					select {
					case old := <-a.networkJobs:
						data, _ := proto.Marshal(old)
						if e := send(&pb.AgentMessage{Msg: &pb.AgentMessage_NetworkResult{NetworkResult: &pb.ApplyResult{Hash: Hash(data), Error: "network configuration superseded"}}}); e != nil {
							return e
						}
					default:
					}
					select {
					case a.networkJobs <- msg.NetworkConfig:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			case *pb.PanelMessage_StatsAck:
				a.acc.Ack(msg.StatsAck.GetSeq())
			default:
				return errors.New("unknown message from panel")
			}
			if e := flush(); e != nil {
				return e
			}
		case <-resultsC:
			if e := flush(); e != nil {
				return e
			}
		case result := <-a.networkResults:
			if e := send(&pb.AgentMessage{Msg: &pb.AgentMessage_NetworkResult{NetworkResult: result}}); e != nil {
				return e
			}
		case <-statsT.C:
			if s := a.acc.Next(); s != nil {
				if err := send(&pb.AgentMessage{Msg: &pb.AgentMessage_Stats{Stats: s}}); err != nil {
					return err
				}
			}
		case <-healthT.C:
			if err := send(a.health(ctx)); err != nil {
				return err
			}
		}
	}
}

func (a *Agent) networkLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case cfg := <-a.networkJobs:
			apply := a.NetworkApplier
			if apply == nil {
				apply = ApplyNetwork
			}
			work, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := apply(work, cfg)
			cancel()
			data, _ := proto.Marshal(cfg)
			result := &pb.ApplyResult{Hash: Hash(data), Ok: err == nil}
			if err != nil {
				result.Error = "network configuration failed"
				a.Log.Error("apply UDP aliases failed")
			}
			select {
			case a.networkResults <- result:
			case <-ctx.Done():
				return
			}
		}
	}
}
func (a *Agent) engineStates() []*pb.EngineState {
	out := make([]*pb.EngineState, 0)
	for _, s := range a.Engines.States() {
		out = append(out, &pb.EngineState{Id: s.ID, Version: s.Version, Active: s.Active, Phase: s.Phase, AppliedHash: s.AppliedHash, RejectedHash: s.RejectedHash, Error: s.Error})
	}
	return out
}

func legacyEngineState(states []*pb.EngineState) *pb.EngineState {
	for _, state := range states {
		if state.GetId() == "sing-box" {
			return state
		}
	}
	return &pb.EngineState{}
}

func (a *Agent) health(_ context.Context) *pb.AgentMessage {
	states := a.engineStates()
	legacy := legacyEngineState(states)
	mem := readKB(a.procDir+"/meminfo", "MemTotal", "MemAvailable")
	self := readKB(a.procDir+"/self/status", "VmRSS")
	return &pb.AgentMessage{Msg: &pb.AgentMessage_Health{Health: &pb.Health{
		UptimeSeconds:     int64(time.Since(a.started).Seconds()),
		Load1:             readLoad1(a.procDir + "/loadavg"),
		MemTotalBytes:     mem["MemTotal"],
		MemAvailableBytes: mem["MemAvailable"],
		AgentRssBytes:     self["VmRSS"],
		SingboxActive:     legacy.Active,
		ConfigHash:        legacy.AppliedHash,
		Engines:           states,
	}}}
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
