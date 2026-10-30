package nodehub

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/artemlavr/giocondo/internal/agent"
	"github.com/artemlavr/giocondo/internal/agent/engine"
	"github.com/artemlavr/giocondo/internal/agent/singboxdriver"
	"github.com/artemlavr/giocondo/internal/nodeapi/pb"
	"github.com/artemlavr/giocondo/internal/nodeapi/pki"
	"github.com/artemlavr/giocondo/internal/nodeapi/v2raystats"
	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
)

// fakeStats is a v2ray_api that returns the given counters once (like sing-box with reset=true).
type fakeStats struct {
	v2raystats.UnimplementedStatsServiceServer
	mu    sync.Mutex
	stats []*v2raystats.Stat
}

func (f *fakeStats) QueryStats(_ context.Context, req *v2raystats.QueryStatsRequest) (*v2raystats.QueryStatsResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	resp := &v2raystats.QueryStatsResponse{Stat: f.stats}
	if req.GetReset_() {
		f.stats = nil
	}
	return resp, nil
}

func (f *fakeStats) set(s ...*v2raystats.Stat) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats = append(f.stats, s...)
}

type testEnv struct {
	st       *store.SQLite
	hub      *Hub
	pki      pki.Paths
	addr     string
	nodeID   int64
	stats    *fakeStats
	statsCli v2raystats.StatsServiceClient
	dir      string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	spec := singbox.Node{Host: "127.0.0.1", Inbounds: []singbox.Inbound{
		{Type: singbox.TypeVLESSReality, Tag: "vless", Port: 443, Reality: &singbox.RealityParams{Dest: "www.microsoft.com:443", ServerName: "www.microsoft.com"}},
		{Type: singbox.TypeHysteria2, Tag: "hy2", Port: 8443},
		{Type: singbox.TypeWireGuard, Tag: "wg", Port: 51820},
	}}
	if err := singbox.Fill(&spec, nil); err != nil {
		t.Fatal(err)
	}
	n := store.Node{Name: "n1", Host: spec.Host}
	if err := st.CreateNode(ctx, &n); err != nil {
		t.Fatal(err)
	}
	for _, in := range spec.Inbounds {
		if err := st.CreateInbound(ctx, &store.Inbound{NodeID: n.ID, Spec: in}); err != nil {
			t.Fatal(err)
		}
	}
	// alice has no WG keys: EnsureWGPeers must issue them while building the config.
	if err := st.CreateUser(ctx, &store.User{Name: "alice", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	p := pki.Paths{Dir: filepath.Join(dir, "pki")}
	if err := pki.InitCA(p.Dir); err != nil {
		t.Fatal(err)
	}
	if err := pki.IssueServer(p.Dir, []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"n1", "ghost"} {
		if err := pki.IssueAgent(p.Dir, name); err != nil {
			t.Fatal(err)
		}
	}
	tlsCfg, err := pki.ServerTLS(p.CACert(), p.ServerCert(), p.ServerKey())
	if err != nil {
		t.Fatal(err)
	}
	hub := &Hub{Store: st, Resync: time.Hour, ApplyInterval: time.Millisecond}
	srv := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsCfg)))
	pb.RegisterNodeServiceServer(srv, hub)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	fs := &fakeStats{}
	statsSrv := grpc.NewServer()
	v2raystats.RegisterStatsServiceServer(statsSrv, fs)
	statsLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = statsSrv.Serve(statsLis) }()
	t.Cleanup(statsSrv.Stop)
	statsConn, err := grpc.NewClient(statsLis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = statsConn.Close() })

	return &testEnv{st: st, hub: hub, pki: p, addr: lis.Addr().String(), nodeID: n.ID,
		stats: fs, statsCli: v2raystats.NewStatsServiceClient(statsConn), dir: dir}
}

func (e *testEnv) panelClient(t *testing.T, node string) pb.NodeServiceClient {
	t.Helper()
	return e.panelClientAt(t, e.addr, node)
}

func (e *testEnv) panelClientAt(t *testing.T, addr, node string) pb.NodeServiceClient {
	t.Helper()
	cfg, err := pki.ClientTLS(e.pki.CACert(), e.pki.AgentCert(node), e.pki.AgentKey(node), "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(cfg)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return pb.NewNodeServiceClient(conn)
}

// newAgent builds an agent with sing-box stubs: check always succeeds, restart/status are true.
func (e *testEnv) newAgent(t *testing.T) (*agent.Agent, string) {
	t.Helper()
	bin := filepath.Join(e.dir, "sing-box")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n[ \"$1\" = version ] && echo 'sing-box version test'\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(e.dir, "node", "config.json")

	runtime := &engine.Runtime{Driver: &singboxdriver.Driver{Binary: bin, RestartCmd: []string{"true"}, StatusCmd: []string{"true"}, StopCmd: []string{"true"}, Collector: &singboxdriver.Collector{Client: e.statsCli}}, ConfigPath: cfgPath, StateDir: filepath.Join(e.dir, "agent-state")}
	if err := runtime.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	engines, err := engine.NewManager(runtime)
	if err != nil {
		t.Fatal(err)
	}
	return &agent.Agent{Panels: []agent.Panel{{Addr: e.addr, Client: e.panelClient(t, "n1")}}, Engines: engines,
		Version:        "test",
		StatsInterval:  50 * time.Millisecond,
		HealthInterval: 50 * time.Millisecond,
	}, cfgPath
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func readFile(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

func TestAgentSync(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	ag, cfgPath := e.newAgent(t)
	actx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- ag.Run(actx) }()

	// 1. The agent receives the config built by the panel and the node becomes online.
	eventually(t, "config on node", func() bool { return strings.Contains(readFile(cfgPath), `"name": "alice"`) })
	want, wantHash, err := e.hub.buildConfig(ctx, e.nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if readFile(cfgPath) != string(want) || agent.Hash([]byte(readFile(cfgPath))) != wantHash {
		t.Error("config on node differs from what panel built")
	}
	eventually(t, "node online", func() bool {
		n, _ := e.st.GetNode(ctx, e.nodeID)
		return n.Status == store.NodeOnline && n.LastSeen != nil
	})
	alice, _ := e.st.GetUser(ctx, 1)
	if alice.WG == nil || alice.WG.Address != "10.70.0.2/32" {
		t.Errorf("alice did not get WG keys: %+v", alice.WG)
	}

	// 2. Traffic from v2ray_api reaches the database exactly once.
	e.stats.set(
		&v2raystats.Stat{Name: "user>>>alice>>>traffic>>>uplink", Value: 100},
		&v2raystats.Stat{Name: "user>>>alice>>>traffic>>>downlink", Value: 200},
		&v2raystats.Stat{Name: "outbound>>>wg-alice>>>traffic>>>downlink", Value: 50},
		&v2raystats.Stat{Name: "user>>>stranger>>>traffic>>>uplink", Value: 7},
	)
	day := time.Now()
	eventually(t, "traffic in store", func() bool {
		u, _ := e.st.UsageByUser(ctx, alice.ID, day, day)
		return u.Up == 100 && u.Down == 250
	})
	time.Sleep(300 * time.Millisecond) // a few more cycles: there must be no repeats
	if u, _ := e.st.UsageByUser(ctx, alice.ID, day, day); u.Up != 100 || u.Down != 250 {
		t.Errorf("traffic counted more than once: %+v", u)
	}

	// 3. A new user and Kick: the node config updates without waiting for Resync.
	if err := e.st.CreateUser(ctx, &store.User{Name: "bob", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	e.hub.Kick()
	eventually(t, "bob on node", func() bool { return strings.Contains(readFile(cfgPath), `"name": "bob"`) })

	// 4. A disabled user disappears from the config.
	alice, _ = e.st.GetUser(ctx, alice.ID)
	alice.Enabled = false
	if err := e.st.UpdateUser(ctx, alice); err != nil {
		t.Fatal(err)
	}
	e.hub.Kick()
	eventually(t, "alice removed", func() bool {
		c := readFile(cfgPath)
		return strings.Contains(c, `"name": "bob"`) && !strings.Contains(c, `"name": "alice"`)
	})
	if _, err := os.Stat(cfgPath + ".prev"); err != nil {
		t.Errorf("no previous config kept: %v", err)
	}

	// 5. The agent is stopped: the node goes offline.
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("agent stopped with %v", err)
	}
	eventually(t, "node offline", func() bool {
		n, _ := e.st.GetNode(ctx, e.nodeID)
		return n.Status == store.NodeOffline
	})
}

func TestAgentFallsBackToSpareAddress(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	// The primary address does not answer: the port is released right after allocation.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := lis.Addr().String()
	_ = lis.Close()

	ag, _ := e.newAgent(t)
	ag.Panels = []agent.Panel{{Addr: dead, Client: e.panelClientAt(t, dead, "n1")}, ag.Panels[0]}
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	start := time.Now()
	finished := make(chan struct{})
	go func() { defer close(finished); _ = ag.Run(actx) }()
	t.Cleanup(func() { cancel(); <-finished })
	eventually(t, "config via spare address", func() bool { return ag.Engines.Runtime("sing-box").Snapshot().AppliedHash != "" })
	// The fallback is tried right after the primary fails, without a backoff pause.
	if d := time.Since(start); d > 900*time.Millisecond {
		t.Errorf("spare address used after %v", d)
	}
}

func TestAgentReconnectSkipsAppliedConfig(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	ag, cfgPath := e.newAgent(t)
	actx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- ag.Run(actx) }()
	eventually(t, "config on node", func() bool { return ag.Engines.Runtime("sing-box").Snapshot().AppliedHash != "" })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("agent stopped with %v", err)
	}
	st1, _ := os.Stat(cfgPath)

	// Second start with the same config: the panel sees the hash in Hello and sends nothing.
	ag2, _ := e.newAgent(t)
	actx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()
	finished2 := make(chan struct{})
	go func() { defer close(finished2); _ = ag2.Run(actx2) }()
	t.Cleanup(func() { cancel2(); <-finished2 })
	eventually(t, "node online", func() bool {
		n, _ := e.st.GetNode(ctx, e.nodeID)
		return n.Status == store.NodeOnline
	})
	time.Sleep(300 * time.Millisecond)
	if st2, _ := os.Stat(cfgPath); !st2.ModTime().Equal(st1.ModTime()) {
		t.Error("config was rewritten although it did not change")
	}
	if _, err := os.Stat(cfgPath + ".prev"); err == nil {
		t.Error("unchanged config produced a .prev backup")
	}
}

func TestApplyThrottle(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	e.hub.ApplyInterval = 500 * time.Millisecond

	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := e.panelClient(t, "n1").Sync(cctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&pb.AgentMessage{Msg: &pb.AgentMessage_Hello{Hello: &pb.Hello{}}}); err != nil {
		t.Fatal(err)
	}
	configs := make(chan string, 10)
	go func() {
		for {
			m, err := stream.Recv()
			if err != nil {
				close(configs)
				return
			}
			if c := m.GetApplyConfig(); c != nil {
				configs <- string(c.GetConfigJson())
			}
		}
	}()
	recv := func(within time.Duration) (string, bool) {
		select {
		case c, ok := <-configs:
			return c, ok
		case <-time.After(within):
			return "", false
		}
	}

	// The first config after Hello goes out without delay.
	if _, ok := recv(5 * time.Second); !ok {
		t.Fatal("no initial config")
	}
	// Three changes in a row: nothing is sent within the interval, then one config with all of them.
	start := time.Now()
	for _, name := range []string{"bob", "carol", "dave"} {
		if err := e.st.CreateUser(ctx, &store.User{Name: name, Enabled: true}); err != nil {
			t.Fatal(err)
		}
		e.hub.Kick()
	}
	c, ok := recv(5 * time.Second)
	if !ok {
		t.Fatal("no config after changes")
	}
	if time.Since(start) < 300*time.Millisecond {
		t.Errorf("config sent after %v, before the apply interval", time.Since(start))
	}
	for _, name := range []string{"bob", "carol", "dave"} {
		if !strings.Contains(c, `"name": "`+name+`"`) {
			t.Errorf("throttled config lacks %s", name)
		}
	}
	if _, ok := recv(time.Second); ok {
		t.Error("changes produced more than one throttled config")
	}
}

func TestSyncRejectsUnknownAndDisabledNodes(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)

	syncErr := func(node string) error {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		stream, err := e.panelClient(t, node).Sync(cctx)
		if err != nil {
			return err
		}
		_ = stream.Send(&pb.AgentMessage{Msg: &pb.AgentMessage_Hello{Hello: &pb.Hello{}}})
		_, err = stream.Recv()
		return err
	}
	if code := status.Code(syncErr("ghost")); code != codes.PermissionDenied {
		t.Errorf("unregistered node: %v", code)
	}

	n, _ := e.st.GetNode(ctx, e.nodeID)
	n.Status = store.NodeDisabled
	if err := e.st.UpdateNode(ctx, n); err != nil {
		t.Fatal(err)
	}
	if code := status.Code(syncErr("n1")); code != codes.PermissionDenied {
		t.Errorf("disabled node: %v", code)
	}
}

func TestSaveStatsDeduplicates(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	batch := &pb.Stats{Seq: 1, Users: []*pb.UserTraffic{{Name: "alice", Up: 10}}}
	for range 3 {
		if err := e.hub.saveStats(ctx, e.nodeID, "n1", "inst-a", batch); err != nil {
			t.Fatal(err)
		}
	}
	// A new agent process restarts seq: this is new data.
	if err := e.hub.saveStats(ctx, e.nodeID, "n1", "inst-b", batch); err != nil {
		t.Fatal(err)
	}
	if u, _ := e.st.UsageByUser(ctx, 1, time.Now(), time.Now()); u.Up != 20 {
		t.Errorf("up = %d, want 20 (one per agent instance)", u.Up)
	}
}

func TestEnsureWGPeers(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	for _, name := range []string{"bob", "carol"} {
		if err := e.st.CreateUser(ctx, &store.User{Name: name, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := EnsureWGPeers(ctx, e.st, singbox.DefaultWGSubnet); err != nil {
		t.Fatal(err)
	}
	users, _ := e.st.ListUsers(ctx)
	seen := map[string]bool{}
	for _, u := range users {
		if u.WG == nil || u.WG.PrivateKey == "" || u.WG.PresharedKey == "" || seen[u.WG.Address] {
			t.Fatalf("bad wg for %s: %+v", u.Name, u.WG)
		}
		if pub, _ := singbox.WGPublicKey(u.WG.PrivateKey); pub != u.WG.PublicKey {
			t.Errorf("%s: public key does not match", u.Name)
		}
		seen[u.WG.Address] = true
	}
	before := users[0].WG.PrivateKey
	if err := EnsureWGPeers(ctx, e.st, singbox.DefaultWGSubnet); err != nil {
		t.Fatal(err)
	}
	if u, _ := e.st.GetUser(ctx, users[0].ID); u.WG.PrivateKey != before {
		t.Error("existing WG keys were replaced")
	}
}

func TestNodeDirtyState(t *testing.T) {
	ctx := context.Background()
	e := newTestEnv(t)
	node := func() *store.Node {
		n, err := e.st.GetNode(ctx, e.nodeID)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Without an agent: Reconcile builds the config and the node is dirty.
	rctx, stopReconcile := context.WithCancel(ctx)
	defer stopReconcile()
	go e.hub.Reconcile(rctx, time.Hour)
	eventually(t, "desired hash", func() bool { return node().DesiredHash != "" })
	if !node().Dirty() {
		t.Fatal("node without agent is not dirty")
	}

	// The agent applied it: not dirty.
	ag, cfgPath := e.newAgent(t)
	actx, stopAgent := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = ag.Run(actx); close(done) }()
	eventually(t, "config applied", func() bool { return readFile(cfgPath) != "" && !node().Dirty() })
	stopAgent()
	<-done

	// A change while the agent is off: Kick wakes Reconcile and the node is dirty again.
	if err := e.st.CreateUser(ctx, &store.User{Name: "bob", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	e.hub.Kick()
	eventually(t, "dirty after change", func() bool { return node().Dirty() })

	// An agent whose sing-box check fails: the error is visible in the panel.
	bad, _ := e.newAgent(t)
	failBin := filepath.Join(e.dir, "sing-box-fail")
	if err := os.WriteFile(failBin, []byte("#!/bin/sh\n[ \"$1\" = check ] && { echo 'bad config' >&2; exit 1; }\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad.Engines.Runtime("sing-box").Driver.(*singboxdriver.Driver).Binary = failBin
	bctx, stopBad := context.WithCancel(ctx)
	defer stopBad()
	badDone := make(chan struct{})
	go func() { defer close(badDone); _ = bad.Run(bctx) }()
	t.Cleanup(func() { stopBad(); <-badDone })
	eventually(t, "apply error", func() bool { return strings.Contains(node().ApplyError, "validation failed") })
	if !node().Dirty() {
		t.Error("node with failed apply is not dirty")
	}
}

func TestEngineRecoveryErrorSurvivesHelloAndHealth(t *testing.T) {
	e := newTestEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, desired, err := e.hub.buildConfig(ctx, e.nodeID)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := e.panelClient(t, "n1").Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state := &pb.EngineState{Id: "sing-box", Phase: "blocked", AppliedHash: "previous", RejectedHash: desired, Error: "rollback failed"}
	if err = stream.Send(&pb.AgentMessage{Msg: &pb.AgentMessage_Hello{Hello: &pb.Hello{ConfigHash: "previous", InstanceId: "runtime-test", Capabilities: []string{"engine-runtime-v1"}, Engines: []*pb.EngineState{state}}}}); err != nil {
		t.Fatal(err)
	}
	if err = stream.Send(&pb.AgentMessage{Msg: &pb.AgentMessage_Health{Health: &pb.Health{ConfigHash: "previous", Engines: []*pb.EngineState{state}}}}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "recovery error retained", func() bool { n, _ := e.st.GetNode(ctx, e.nodeID); return n != nil && n.ApplyError == "rollback failed" })
	cancel()
}

func TestAgentUpgradePreservesImportedWireGuard(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	config, hash, err := e.hub.buildConfig(ctx, e.nodeID)
	if err != nil {
		t.Fatal(err)
	}
	users, err := e.st.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inbounds, err := e.st.ListInbounds(ctx, e.nodeID)
	if err != nil {
		t.Fatal(err)
	}
	ag, path := e.newAgent(t)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, config, 0600); err != nil {
		t.Fatal(err)
	}
	restartLog := filepath.Join(e.dir, "unexpected-restart")
	restart := filepath.Join(e.dir, "record-restart")
	if err = os.WriteFile(restart, []byte("#!/bin/sh\ntouch '"+restartLog+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ag.Engines.Runtime("sing-box").Driver.(*singboxdriver.Driver).RestartCmd = []string{restart}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- ag.Run(runCtx) }()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("agent shutdown: %v", err)
		}
	}()
	eventually(t, "existing config adopted", func() bool { n, _ := e.st.GetNode(ctx, e.nodeID); return n.AppliedHash == hash })
	time.Sleep(150 * time.Millisecond)
	if _, err = os.Stat(restartLog); !os.IsNotExist(err) {
		t.Fatal("upgrade restarted sing-box")
	}
	if got := readFile(path); got != string(config) {
		t.Fatal("upgrade changed server config")
	}
	gotUsers, _ := e.st.ListUsers(ctx)
	gotInbounds, _ := e.st.ListInbounds(ctx, e.nodeID)
	if !reflect.DeepEqual(users, gotUsers) || !reflect.DeepEqual(inbounds, gotInbounds) {
		t.Fatal("upgrade changed imported credentials or profiles")
	}
}
