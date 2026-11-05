package singboxctl

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/artemlavr/giocondo/internal/service/engine"
	"github.com/artemlavr/giocondo/internal/transport/grpc/v2raystats"
)

// wgOutboundPrefix matches singbox.WGOutboundPrefix: WireGuard peer traffic is counted
// by its outbound "wg-<name>". The agent does not need the singbox package, so the constant is repeated.
const wgOutboundPrefix = "wg-"

// DialStats connects to the sing-box v2ray_api endpoint. It listens on loopback only, so no TLS is used.
// The connection is lazy; the returned function closes it.
func DialStats(addr string) (v2raystats.StatsServiceClient, func() error, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}

	return v2raystats.NewStatsServiceClient(conn), conn.Close, nil
}

// Collect returns per-user traffic since the previous call and resets the counters in sing-box.
// A driver without a statistics client reports no traffic.
func (d *Driver) Collect(ctx context.Context) (map[string]engine.Traffic, error) {
	if d.Stats == nil {
		return nil, nil
	}

	resp, err := d.Stats.QueryStats(ctx, &v2raystats.QueryStatsRequest{Reset_: true})
	if err != nil {
		return nil, err
	}

	return parseStats(resp.GetStat()), nil
}

// parseStats turns sing-box counters into per-user traffic:
//
//	user>>>NAME>>>traffic>>>uplink|downlink        (VLESS, Hysteria2)
//	outbound>>>wg-NAME>>>traffic>>>uplink|downlink (WireGuard)
//
// Other counters are skipped.
func parseStats(stats []*v2raystats.Stat) map[string]engine.Traffic {
	out := map[string]engine.Traffic{}

	for _, s := range stats {
		parts := strings.Split(s.GetName(), ">>>")
		if len(parts) != 4 || parts[2] != "traffic" || s.GetValue() <= 0 {
			continue
		}

		var name string

		switch {
		case parts[0] == "user":
			name = parts[1]
		case parts[0] == "outbound" && strings.HasPrefix(parts[1], wgOutboundPrefix):
			name = strings.TrimPrefix(parts[1], wgOutboundPrefix)
		default:
			continue
		}

		if name == "" {
			continue
		}

		t := out[name]

		switch parts[3] {
		case "uplink":
			t.Up += s.GetValue()
		case "downlink":
			t.Down += s.GetValue()
		default:
			continue
		}

		out[name] = t
	}

	return out
}
