package agent

import (
	"sort"
	"sync"

	"github.com/artemlavr/giocondo/internal/agent/engine"
	"github.com/artemlavr/giocondo/internal/nodeapi/pb"
)

type Traffic = engine.Traffic

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
