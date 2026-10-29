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
	Applier        *Applier
	Collector      *Collector // nil means statistics are not collected
	Version        string
	StatsInterval  time.Duration
	HealthInterval time.Duration
	Log            *slog.Logger

	acc        Accumulator
	instanceID string
	started    time.Time
	procDir    string // "/proc"; replaced in tests
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
	if a.Collector != nil {
		prev := a.Applier.BeforeRestart
		a.Applier.BeforeRestart = func(ctx context.Context) {
			// Restarting sing-box resets the counters: collect them first.
			a.collect(ctx)
			if prev != nil {
				prev(ctx)
			}
		}
	}
}

// Run works until ctx is cancelled: connects to the panel and reconnects after drops.
// The address is sticky: after a long session drops the agent returns to the same address, after a failure
// it tries the next one at once; the backoff pause happens only when all addresses failed in a row.
func (a *Agent) Run(ctx context.Context) error {
	if len(a.Panels) == 0 {
		return errors.New("no panel addresses")
	}
	a.init()
	if a.Collector != nil {
		go a.collectLoop(ctx)
	}
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

func (a *Agent) collectLoop(ctx context.Context) {
	t := time.NewTicker(a.StatsInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.collect(ctx)
		}
	}
}

func (a *Agent) collect(ctx context.Context) {
	if a.Collector == nil {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	t, err := a.Collector.Collect(cctx)
	if err != nil {
		a.Log.Debug("collect stats", "err", err) // sing-box may not be running yet
		return
	}
	a.acc.Add(t)
}

func (a *Agent) session(ctx context.Context, p Panel) error {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := p.Client.Sync(sctx)
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}
	send := func(m *pb.AgentMessage) error { return stream.Send(m) }

	hello := &pb.Hello{
		AgentVersion:   a.Version,
		SingboxVersion: a.Applier.Version(ctx),
		ConfigHash:     a.Applier.CurrentHash(),
		InstanceId:     a.instanceID,
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

	for {
		select {
		case <-ctx.Done():
			_ = stream.CloseSend()
			return ctx.Err()
		case err := <-recvErr:
			return err
		case m := <-recv:
			if err := a.handle(ctx, m, send); err != nil {
				return err
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

func (a *Agent) handle(ctx context.Context, m *pb.PanelMessage, send func(*pb.AgentMessage) error) error {
	switch msg := m.GetMsg().(type) {
	case *pb.PanelMessage_NetworkConfig:
		data, _ := proto.Marshal(msg.NetworkConfig)
		hash := Hash(data)
		apply := a.NetworkApplier
		if apply == nil {
			apply = ApplyNetwork
		}
		err := apply(ctx, msg.NetworkConfig)
		result := &pb.ApplyResult{Hash: hash, Ok: err == nil}
		if err != nil {
			result.Error = err.Error()
			a.Log.Error("apply UDP aliases", "err", err)
		}
		return send(&pb.AgentMessage{Msg: &pb.AgentMessage_NetworkResult{NetworkResult: result}})
	case *pb.PanelMessage_ApplyConfig:
		cfg := msg.ApplyConfig
		res := &pb.ApplyResult{Hash: cfg.GetHash(), Ok: true}
		var err error
		if got := Hash(cfg.GetConfigJson()); got != cfg.GetHash() {
			err = fmt.Errorf("hash mismatch: got %s, want %s", short(got), short(cfg.GetHash()))
		} else {
			err = a.Applier.Apply(ctx, cfg.GetConfigJson())
		}
		if err != nil {
			res.Ok, res.Error = false, err.Error()
			a.Log.Error("apply config", "hash", short(cfg.GetHash()), "err", err)
		} else {
			a.Log.Info("config applied", "hash", short(cfg.GetHash()))
		}
		if err := send(&pb.AgentMessage{Msg: &pb.AgentMessage_ApplyResult{ApplyResult: res}}); err != nil {
			return err
		}
		return send(a.health(ctx))
	case *pb.PanelMessage_StatsAck:
		a.acc.Ack(msg.StatsAck.GetSeq())
		return nil
	default:
		return errors.New("unknown message from panel")
	}
}

func (a *Agent) health(ctx context.Context) *pb.AgentMessage {
	mem := readKB(a.procDir+"/meminfo", "MemTotal", "MemAvailable")
	self := readKB(a.procDir+"/self/status", "VmRSS")
	return &pb.AgentMessage{Msg: &pb.AgentMessage_Health{Health: &pb.Health{
		UptimeSeconds:     int64(time.Since(a.started).Seconds()),
		Load1:             readLoad1(a.procDir + "/loadavg"),
		MemTotalBytes:     mem["MemTotal"],
		MemAvailableBytes: mem["MemAvailable"],
		AgentRssBytes:     self["VmRSS"],
		SingboxActive:     a.Applier.Active(ctx),
		ConfigHash:        a.Applier.CurrentHash(),
	}}}
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
