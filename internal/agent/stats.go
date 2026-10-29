package agent

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/artemlavr/giocondo/internal/nodeapi/pb"
	"github.com/artemlavr/giocondo/internal/nodeapi/v2raystats"
)

// wgOutboundPrefix matches singbox.WGOutboundPrefix: WireGuard peer traffic is counted
// by its outbound "wg-<name>". The agent does not need the singbox package, so the constant is repeated.
const wgOutboundPrefix = "wg-"

// Traffic is bytes up/down.
type Traffic struct{ Up, Down int64 }

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

// Accumulator holds traffic until the panel acknowledges it. The in-flight batch
// is repeated with the same seq until an ack arrives; new traffic accumulates separately meanwhile.
type Accumulator struct {
	mu       sync.Mutex
	pending  map[string]Traffic
	inflight *pb.Stats
	seq      uint64
}

// Add adds traffic to what is waiting to be sent.
func (a *Accumulator) Add(t map[string]Traffic) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pending == nil {
		a.pending = map[string]Traffic{}
	}
	for name, v := range t {
		cur := a.pending[name]
		cur.Up += v.Up
		cur.Down += v.Down
		a.pending[name] = cur
	}
}

// Next returns the batch to send: the unacknowledged one if any, otherwise a new one from
// the accumulated traffic. nil means there is nothing to send.
func (a *Accumulator) Next() *pb.Stats {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inflight != nil {
		return a.inflight
	}
	if len(a.pending) == 0 {
		return nil
	}
	a.seq++
	names := make([]string, 0, len(a.pending))
	for n := range a.pending {
		names = append(names, n)
	}
	sort.Strings(names)
	msg := &pb.Stats{Seq: a.seq}
	for _, n := range names {
		v := a.pending[n]
		msg.Users = append(msg.Users, &pb.UserTraffic{Name: n, Up: v.Up, Down: v.Down})
	}
	a.inflight = msg
	a.pending = nil
	return msg
}

// Ack clears the in-flight batch if seq matches.
func (a *Accumulator) Ack(seq uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.inflight != nil && a.inflight.Seq == seq {
		a.inflight = nil
	}
}
