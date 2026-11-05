package reconciliation

import (
	"context"
	"testing"
	"time"
)

type configSource struct {
	kick         chan struct{}
	passes       chan struct{}
	unsubscribed chan struct{}
}

func (s *configSource) SubscribeReconciliation() (<-chan struct{}, func()) {
	return s.kick, func() { close(s.unsubscribed) }
}

func (s *configSource) ReconcileOnce(ctx context.Context) {
	select {
	case s.passes <- struct{}{}:
	case <-ctx.Done():
	}
}

func TestRunReconcilesInitiallyOnKickAndUnsubscribes(t *testing.T) {
	s := &configSource{kick: make(chan struct{}, 1), passes: make(chan struct{}, 2), unsubscribed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &Worker{Source: s, Interval: time.Hour}
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	select {
	case <-s.passes:
	case <-time.After(time.Second):
		t.Fatal("initial reconciliation missing")
	}
	s.kick <- struct{}{}
	select {
	case <-s.passes:
	case <-time.After(time.Second):
		t.Fatal("Kick did not reconcile")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	select {
	case <-s.unsubscribed:
	default:
		t.Fatal("subscription leaked")
	}
}
