// Package panelsync runs the agent side of the Sync stream: it connects to the panel, executes its
// commands and reports applies, health and traffic.
package panelsync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
	"github.com/artemlavr/giocondo/internal/worker/enginequeue"
	"github.com/artemlavr/giocondo/internal/worker/netqueue"
	"github.com/artemlavr/giocondo/pkg/logger"
)

const (
	backoffFirst = time.Second
	backoffLimit = time.Minute

	// A session that lived longer than this ended by a drop, not by an unreachable address.
	longSession = 30 * time.Second
)

// Connector opens a Sync stream to one panel address.
type Connector interface {
	Sync(context.Context) (nodesync.Stream, error)
}

// Panel is one of the panel addresses.
type Panel struct {
	Addr string // for logs
	Conn Connector
}

// Syncer holds the Sync stream with the panel. Its fields are configuration and are not changed by
// Run. Engines, Network and Traffic outlive sessions: an apply keeps running and unacknowledged
// traffic is resent after a reconnect. The caller starts and joins the engine and network workers;
// the syncer only submits work to them and reports the results.
type Syncer struct {
	Panels         []Panel // primary address and fallbacks, in order of preference
	Engines        *enginequeue.Manager
	Network        *netqueue.Worker
	Traffic        *TrafficBuffer // filled by the engines through OnTraffic
	ReadHost       func() HostMetrics
	Version        string
	StatsInterval  time.Duration
	HealthInterval time.Duration
}

// instance identifies one Run to the panel, which deduplicates traffic batches by instance ID and seq.
type instance struct {
	id      string
	started time.Time
}

func newInstance() instance {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // never fails since Go 1.24

	return instance{id: hex.EncodeToString(b), started: time.Now()}
}

// Run works until ctx is cancelled: connects to the panel and reconnects after drops.
// The address is sticky: after a long session drops the agent returns to the same address, after a failure
// it tries the next one at once; the backoff pause happens only when all addresses failed in a row.
func (s *Syncer) Run(ctx context.Context) error {
	if len(s.Panels) == 0 {
		return errors.New("no panel addresses")
	}

	if s.Engines == nil || s.Traffic == nil || s.Network == nil {
		return errors.New("engines, traffic buffer and network worker are required")
	}

	if s.StatsInterval <= 0 || s.HealthInterval <= 0 {
		return errors.New("stats and health intervals are required")
	}

	inst := newInstance()
	backoff := backoffFirst

	cur, failed := 0, 0 // failed counts consecutive failures since the last long session
	for {
		p := s.Panels[cur]
		start := time.Now()

		err := s.session(logger.With(ctx, "panel", p.Addr), p, inst)
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if time.Since(start) > longSession {
			backoff, failed = backoffFirst, 0
		} else {
			failed++

			cur = (cur + 1) % len(s.Panels)
			if failed%len(s.Panels) != 0 {
				logger.FromCtx(ctx).Warn("panel unavailable, trying next address",
					"addr", p.Addr, "err", err, "next", s.Panels[cur].Addr)

				continue
			}
		}

		logger.FromCtx(ctx).Warn("panel session ended", "addr", p.Addr, "err", err, "retry_in", backoff)

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

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}

	return h
}
