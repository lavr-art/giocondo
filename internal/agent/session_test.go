package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/agent/engine"
	"github.com/artemlavr/giocondo/internal/nodeapi/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type blockingDriver struct {
	start, release chan struct{}
	once           sync.Once
	restarts       atomic.Int32
}

func (d *blockingDriver) ID() string                              { return "sing-box" }
func (d *blockingDriver) Version(context.Context) (string, error) { return "test", nil }
func (d *blockingDriver) Validate(context.Context, string) error  { return nil }
func (d *blockingDriver) Restart(ctx context.Context) error {
	d.restarts.Add(1)
	d.once.Do(func() { close(d.start) })
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-d.release:
		return nil
	}
}
func (d *blockingDriver) Stop(context.Context) error           { return nil }
func (d *blockingDriver) Active(context.Context) (bool, error) { return true, nil }
func (d *blockingDriver) Collect(context.Context) (map[string]engine.Traffic, error) {
	return map[string]engine.Traffic{"alice": {Up: 7, Down: 11}}, nil
}

type streamServer struct {
	pb.UnimplementedNodeServiceServer
	fn func(pb.NodeService_SyncServer) error
}

func (s *streamServer) Sync(stream pb.NodeService_SyncServer) error { return s.fn(stream) }

func TestSyncRemainsResponsiveDuringApplyAndNetwork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	old := []byte("old WG keys")
	if e := os.WriteFile(config, old, 0600); e != nil {
		t.Fatal(e)
	}
	d := &blockingDriver{start: make(chan struct{}), release: make(chan struct{})}
	r := &engine.Runtime{Driver: d, ConfigPath: config, StateDir: filepath.Join(dir, "state")}
	if e := r.Open(ctx); e != nil {
		t.Fatal(e)
	}
	m, e := engine.NewManager(r)
	if e != nil {
		t.Fatal(e)
	}
	a := &Agent{Engines: m, StatsInterval: 20 * time.Millisecond, HealthInterval: 20 * time.Millisecond, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	networkStarted := make(chan struct{})
	networkRelease := make(chan struct{})
	a.NetworkApplier = func(ctx context.Context, _ *pb.NetworkConfig) error {
		close(networkStarted)
		select {
		case <-networkRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan error, 1)
	server := grpc.NewServer()
	pb.RegisterNodeServiceServer(server, &streamServer{fn: func(stream pb.NodeService_SyncServer) error {
		hello, e := stream.Recv()
		if e != nil {
			return e
		}
		if hello.GetHello().GetConfigHash() != Hash(old) || len(hello.GetHello().GetCapabilities()) != 1 {
			return errors.New("legacy hello changed")
		}
		data := []byte("new")
		if e = stream.Send(&pb.PanelMessage{Msg: &pb.PanelMessage_ApplyConfig{ApplyConfig: &pb.ApplyConfig{ConfigJson: data, Hash: Hash(data)}}}); e != nil {
			return e
		}
		if e = stream.Send(&pb.PanelMessage{Msg: &pb.PanelMessage_NetworkConfig{NetworkConfig: &pb.NetworkConfig{PrimaryIp: "192.0.2.1"}}}); e != nil {
			return e
		}
		select {
		case <-d.start:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-networkStarted:
		case <-ctx.Done():
			return ctx.Err()
		}
		gotHealth, gotStats := false, false
		for !gotHealth || !gotStats {
			msg, e := stream.Recv()
			if e != nil {
				return e
			}
			if health := msg.GetHealth(); health != nil && health.GetEngines()[0].GetPhase() == "applying" {
				if health.GetConfigHash() != Hash(old) {
					return errors.New("uncommitted hash leaked")
				}
				gotHealth = true
			}
			if stats := msg.GetStats(); stats != nil {
				gotStats = true
				if stats.GetUsers()[0].GetUp() != 7 {
					return errors.New("traffic lost")
				}
				if e = stream.Send(&pb.PanelMessage{Msg: &pb.PanelMessage_StatsAck{StatsAck: &pb.StatsAck{Seq: stats.GetSeq()}}}); e != nil {
					return e
				}
			}
		}
		close(d.release)
		close(networkRelease)
		for {
			msg, e := stream.Recv()
			if e != nil {
				return e
			}
			if result := msg.GetApplyResult(); result != nil {
				if !result.GetOk() || result.GetHash() != Hash(data) {
					return errors.New("apply failed")
				}
				done <- nil
				return nil
			}
		}
	}})
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()
	conn, e := grpc.NewClient("passthrough:///bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = conn.Close() }()
	a.Panels = []Panel{{Addr: "test", Client: pb.NewNodeServiceClient(conn)}}
	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(ctx) }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("Sync blocked during external operations")
	}
	cancel()
	if err := <-runDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("agent shutdown: %v", err)
	}
}

func TestApplySurvivesSyncReconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	dir := t.TempDir()
	config := filepath.Join(dir, "config.json")
	old := []byte("WG before reconnect")
	data := []byte("new")
	if e := os.WriteFile(config, old, 0600); e != nil {
		t.Fatal(e)
	}
	d := &blockingDriver{start: make(chan struct{}), release: make(chan struct{})}
	r := &engine.Runtime{Driver: d, ConfigPath: config, StateDir: filepath.Join(dir, "state")}
	m, e := engine.NewManager(r)
	if e != nil {
		t.Fatal(e)
	}
	var sessions atomic.Int32
	result := make(chan error, 1)
	server := grpc.NewServer()
	pb.RegisterNodeServiceServer(server, &streamServer{fn: func(stream pb.NodeService_SyncServer) error {
		first, e := stream.Recv()
		if e != nil {
			return e
		}
		n := sessions.Add(1)
		if first.GetHello().GetConfigHash() != Hash(old) {
			result <- errors.New("lost confirmed hash on reconnect")
			return nil
		}
		if e = stream.Send(&pb.PanelMessage{Msg: &pb.PanelMessage_ApplyConfig{ApplyConfig: &pb.ApplyConfig{ConfigJson: data, Hash: Hash(data)}}}); e != nil {
			return e
		}
		if n == 1 {
			select {
			case <-d.start:
				return status.Error(codes.Unavailable, "test disconnect")
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if first.GetHello().GetEngines()[0].GetPhase() != "applying" {
			result <- errors.New("apply was cancelled with Sync")
			return nil
		}
		close(d.release)
		for {
			msg, e := stream.Recv()
			if e != nil {
				return e
			}
			if applied := msg.GetApplyResult(); applied != nil {
				if !applied.GetOk() {
					result <- errors.New(applied.GetError())
				} else {
					result <- nil
				}
				return nil
			}
		}
	}})
	lis := bufconn.Listen(1024 * 1024)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()
	conn, e := grpc.NewClient("passthrough:///reconnect", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = conn.Close() }()
	a := &Agent{Panels: []Panel{{Addr: "test", Client: pb.NewNodeServiceClient(conn)}}, Engines: m, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	select {
	case e := <-result:
		if e != nil {
			t.Error(e)
		}
	case <-ctx.Done():
		t.Error("reconnected apply did not complete")
	}
	cancel()
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if d.restarts.Load() != 1 {
		t.Fatalf("restarted %d times", d.restarts.Load())
	}
}
