package watchsched

import (
	"context"
	"errors"
	"testing"
	"time"
)

type statusPass func(context.Context) error

func (f statusPass) WatchOnce(ctx context.Context) error { return f(ctx) }

func TestRunChecksImmediatelyAndContinuesAfterError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan struct{}, 4)
	w := &Worker{Interval: time.Millisecond, Service: statusPass(func(ctx context.Context) error {
		select {
		case calls <- struct{}{}:
		case <-ctx.Done():
		}
		return errors.New("temporary failure")
	})}
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	for range 2 {
		select {
		case <-calls:
		case <-time.After(time.Second):
			t.Fatal("checks did not continue")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker ignored cancellation")
	}
}
