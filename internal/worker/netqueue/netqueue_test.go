package netqueue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
)

func awaitResult(t *testing.T, w *Worker) *nodesync.ApplyResult {
	t.Helper()
	select {
	case result := <-w.Results():
		return result
	case <-time.After(time.Second):
		t.Fatal("missing network result")
		return nil
	}
}

func TestApplyResultAndCancellation(t *testing.T) {
	for _, fail := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		worker := New(func(context.Context, *nodesync.NetworkConfig) error {
			if fail {
				return errors.New("secret")
			}
			return nil
		})
		done := make(chan struct{})
		go func() { worker.Run(ctx); close(done) }()

		worker.Submit(&nodesync.NetworkConfig{Hash: "config-hash"})
		result := awaitResult(t, worker)
		if result.Hash != "config-hash" || result.Ok == fail {
			t.Fatalf("result: %+v", result)
		}
		if fail && result.Error != "network configuration failed" {
			t.Fatalf("unsafe error: %q", result.Error)
		}

		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("worker did not stop")
		}
	}
}

func TestSubmitReplacesQueuedCommand(t *testing.T) {
	var applied []string
	worker := New(func(_ context.Context, cfg *nodesync.NetworkConfig) error {
		applied = append(applied, cfg.Hash)
		return nil
	})

	if old := worker.Submit(&nodesync.NetworkConfig{Hash: "one"}); old != nil {
		t.Fatalf("empty queue replaced %+v", old)
	}
	if old := worker.Submit(&nodesync.NetworkConfig{Hash: "two"}); old == nil || old.Hash != "one" {
		t.Fatalf("replaced %+v, want the queued command one", old)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()

	if result := awaitResult(t, worker); result.Hash != "two" || !result.Ok {
		t.Fatalf("result %+v, want two applied", result)
	}
	cancel()
	<-done

	if len(applied) != 1 || applied[0] != "two" {
		t.Fatalf("applied %v, want only the newest command", applied)
	}
}

func TestCancellationWaitsForActiveApply(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := New(func(ctx context.Context, _ *nodesync.NetworkConfig) error {
		close(entered)
		<-ctx.Done()
		<-release
		return ctx.Err()
	})
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()

	worker.Submit(&nodesync.NetworkConfig{Hash: "h"})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("apply did not start")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("worker left apply running")
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}
