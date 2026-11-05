package panelsync

import (
	"sort"
	"sync"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
	"github.com/artemlavr/giocondo/internal/service/engine"
)

// TrafficBuffer holds collected traffic until the panel acknowledges it. It outlives Sync sessions,
// so a batch left unacknowledged by a dropped session is resent after reconnect. The in-flight batch
// is repeated with the same seq until its ack arrives, which lets the panel deduplicate it; new traffic
// accumulates separately meanwhile. The zero value is ready to use and safe for concurrent use.
type TrafficBuffer struct {
	mu       sync.Mutex
	pending  map[string]engine.Traffic
	inflight *nodesync.Stats
	seq      uint64
}

// Add queues traffic for the next batch. It is the engines' OnTraffic hook.
func (a *TrafficBuffer) Add(t map[string]engine.Traffic) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.pending == nil {
		a.pending = map[string]engine.Traffic{}
	}

	for name, v := range t {
		cur := a.pending[name]
		cur.Up += v.Up
		cur.Down += v.Down
		a.pending[name] = cur
	}
}

// next returns the batch to send: the unacknowledged one if any, otherwise a new one from
// the accumulated traffic. nil means there is nothing to send.
func (a *TrafficBuffer) next() *nodesync.Stats {
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

	msg := &nodesync.Stats{Seq: a.seq}

	for _, n := range names {
		v := a.pending[n]
		msg.Users = append(msg.Users, &nodesync.UserTraffic{Name: n, Up: v.Up, Down: v.Down})
	}

	a.inflight = msg
	a.pending = nil

	return msg
}

// ack clears the in-flight batch if seq matches.
func (a *TrafficBuffer) ack(seq uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.inflight != nil && a.inflight.Seq == seq {
		a.inflight = nil
	}
}
