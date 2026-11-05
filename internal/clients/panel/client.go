// Package panel implements the agent's outgoing panel connection and wire mapping.
package panel

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/protobuf/proto"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
	"github.com/artemlavr/giocondo/internal/transport/grpc/pb"
)

// Client is the agent's gRPC connection to one panel address.
type Client struct {
	raw   pb.NodeServiceClient
	close func() error
}

// New wraps an existing gRPC client; Close then does not close its connection.
func New(raw pb.NodeServiceClient) *Client { return &Client{raw: raw} }

// Dial prepares a connection to addr with mTLS and keepalives. The connection is lazy: nothing is
// dialed until the first Sync.
func Dial(addr string, tlsConfig *tls.Config) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{Time: 30 * time.Second, Timeout: 20 * time.Second}),
		grpc.WithConnectParams(
			grpc.ConnectParams{
				Backoff:           backoff.Config{BaseDelay: time.Second, Multiplier: 1.6, Jitter: 0.2, MaxDelay: time.Minute},
				MinConnectTimeout: 20 * time.Second,
			},
		))
	if err != nil {
		return nil, err
	}

	return &Client{raw: pb.NewNodeServiceClient(conn), close: conn.Close}, nil
}

// Close closes the connection opened by Dial.
func (c *Client) Close() error {
	if c.close != nil {
		return c.close()
	}

	return nil
}

// Sync opens a Sync stream that lives until ctx ends or the stream fails.
func (c *Client) Sync(ctx context.Context) (nodesync.Stream, error) {
	s, e := c.raw.Sync(ctx)
	if e != nil {
		return nil, e
	}

	return &stream{raw: s}, nil
}

type stream struct{ raw pb.NodeService_SyncClient }

func (s *stream) CloseSend() error { return s.raw.CloseSend() }

func (s *stream) Send(m *nodesync.AgentMessage) error {
	var out pb.AgentMessage

	switch {
	case m.Hello != nil:
		out.Msg = &pb.AgentMessage_Hello{Hello: encodeHello(m.Hello)}
	case m.Health != nil:
		out.Msg = &pb.AgentMessage_Health{Health: encodeHealth(m.Health)}
	case m.Stats != nil:
		out.Msg = &pb.AgentMessage_Stats{Stats: encodeStats(m.Stats)}
	case m.ApplyResult != nil:
		out.Msg = &pb.AgentMessage_ApplyResult{ApplyResult: encodeApplyResult(m.ApplyResult)}
	case m.NetworkResult != nil:
		out.Msg = &pb.AgentMessage_NetworkResult{NetworkResult: encodeApplyResult(m.NetworkResult)}
	default:
		return errors.New("empty agent message")
	}

	return s.raw.Send(&out)
}

func (s *stream) Recv() (*nodesync.PanelMessage, error) {
	m, e := s.raw.Recv()
	if e != nil {
		return nil, e
	}

	switch v := m.GetMsg().(type) {
	case *pb.PanelMessage_ApplyConfig:
		return &nodesync.PanelMessage{
			ApplyConfig: &nodesync.ApplyConfig{ConfigJSON: v.ApplyConfig.GetConfigJson(), Hash: v.ApplyConfig.GetHash()},
		}, nil
	case *pb.PanelMessage_StatsAck:
		return &nodesync.PanelMessage{StatsAck: &nodesync.StatsAck{Seq: v.StatsAck.GetSeq()}}, nil
	case *pb.PanelMessage_NetworkConfig:
		data, e := proto.Marshal(v.NetworkConfig)
		if e != nil {
			return nil, e
		}

		h := sha256.Sum256(data)

		return &nodesync.PanelMessage{
			NetworkConfig: &nodesync.NetworkConfig{
				PrimaryIP: v.NetworkConfig.GetPrimaryIp(),
				Aliases:   v.NetworkConfig.GetAliases(),
				UDPPorts:  v.NetworkConfig.GetUdpPorts(),
				Hash:      hex.EncodeToString(h[:]),
			},
		}, nil
	default:
		return nil, errors.New("unknown message from panel")
	}
}

func encodeHello(v *nodesync.Hello) *pb.Hello {
	if v == nil {
		return nil
	}

	return &pb.Hello{
		AgentVersion:   v.AgentVersion,
		SingboxVersion: v.SingboxVersion,
		ConfigHash:     v.ConfigHash,
		InstanceId:     v.InstanceID,
		Capabilities:   v.Capabilities,
		Engines:        encodeEngineStates(v.Engines),
	}
}

func encodeHealth(v *nodesync.Health) *pb.Health {
	if v == nil {
		return nil
	}

	return &pb.Health{
		UptimeSeconds:     v.UptimeSeconds,
		Load1:             v.Load1,
		MemTotalBytes:     v.MemTotalBytes,
		MemAvailableBytes: v.MemAvailableBytes,
		AgentRssBytes:     v.AgentRssBytes,
		SingboxActive:     v.SingboxActive,
		ConfigHash:        v.ConfigHash,
		Engines:           encodeEngineStates(v.Engines),
	}
}

func encodeStats(v *nodesync.Stats) *pb.Stats {
	if v == nil {
		return nil
	}

	return &pb.Stats{Seq: v.Seq, Users: encodeUserTraffics(v.Users)}
}

func encodeUserTraffic(v *nodesync.UserTraffic) *pb.UserTraffic {
	if v == nil {
		return nil
	}

	return &pb.UserTraffic{Name: v.Name, Up: v.Up, Down: v.Down}
}

func encodeApplyResult(v *nodesync.ApplyResult) *pb.ApplyResult {
	if v == nil {
		return nil
	}

	return &pb.ApplyResult{Hash: v.Hash, Ok: v.Ok, Error: v.Error}
}

func encodeEngineState(v *nodesync.EngineState) *pb.EngineState {
	if v == nil {
		return nil
	}

	return &pb.EngineState{
		Id:           v.ID,
		Version:      v.Version,
		Active:       v.Active,
		Phase:        v.Phase,
		AppliedHash:  v.AppliedHash,
		RejectedHash: v.RejectedHash,
		Error:        v.Error,
	}
}

func encodeEngineStates(v []*nodesync.EngineState) []*pb.EngineState {
	out := make([]*pb.EngineState, 0, len(v))
	for _, x := range v {
		out = append(out, encodeEngineState(x))
	}

	return out
}

func encodeUserTraffics(v []*nodesync.UserTraffic) []*pb.UserTraffic {
	out := make([]*pb.UserTraffic, 0, len(v))
	for _, x := range v {
		out = append(out, encodeUserTraffic(x))
	}

	return out
}
