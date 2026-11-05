package healthsched

import (
	"context"
	"testing"
	"time"
)

type checkPass func(context.Context) error

func (f checkPass) CheckNodesOnce(ctx context.Context) error { return f(ctx) }

func TestRunWaitsForActiveCheckOnCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &Worker{Interval: time.Hour, Service: checkPass(func(ctx context.Context) error { close(entered); <-ctx.Done(); <-release; return ctx.Err() })}
	done := make(chan struct{})
	go func() { defer close(done); w.Run(ctx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("initial check did not start")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("worker returned before check cleanup")
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not finish")
	}
}
