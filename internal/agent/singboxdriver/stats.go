package singboxdriver

import (
	"context"
	"strings"

	"github.com/artemlavr/giocondo/internal/agent/engine"
	"github.com/artemlavr/giocondo/internal/nodeapi/v2raystats"
)

// wgOutboundPrefix matches singbox.WGOutboundPrefix: WireGuard peer traffic is counted
// by its outbound "wg-<name>". The agent does not need the singbox package, so the constant is repeated.
const wgOutboundPrefix = "wg-"

// Traffic is bytes up/down.
type Traffic = engine.Traffic

// ParseStats turns sing-box counters into per-user traffic:
//
//	user>>>NAME>>>traffic>>>uplink|downlink        (VLESS, Hysteria2)
//	outbound>>>wg-NAME>>>traffic>>>uplink|downlink (WireGuard)
//
// Other counters are skipped.
func ParseStats(stats []*v2raystats.Stat) map[string]Traffic {
	out := map[string]Traffic{}
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

// Collector reads sing-box statistics and resets the counters.
type Collector struct {
	Client v2raystats.StatsServiceClient
}

// Collect returns traffic since the previous call and resets the counters in sing-box.
func (c *Collector) Collect(ctx context.Context) (map[string]Traffic, error) {
	resp, err := c.Client.QueryStats(ctx, &v2raystats.QueryStatsRequest{Reset_: true})
	if err != nil {
		return nil, err
	}
	return ParseStats(resp.GetStat()), nil
}
