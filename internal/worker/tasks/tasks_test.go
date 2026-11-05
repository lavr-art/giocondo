package tasks

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStopCancelsWaitsAndRejectsTasks(t *testing.T) {
	e := New(context.Background())
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	if !e.Submit(time.Hour, func(ctx context.Context) { close(entered); <-ctx.Done(); close(canceled); <-release }) {
		t.Fatal("task rejected")
	}
	<-entered
	stopped := make(chan struct{})
	go func() { e.Close(); close(stopped) }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("task not canceled")
	}
	select {
	case <-stopped:
		t.Fatal("Close returned before task cleanup")
	default:
	}
	if e.Submit(time.Hour, func(context.Context) { t.Error("task ran after Close") }) {
		t.Fatal("accepted after Close")
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish")
	}
	e.Close()
}

func TestTaskTimeout(t *testing.T) {
	e := New(context.Background())
	defer e.Close()
	done := make(chan error, 1)
	e.Submit(time.Millisecond, func(ctx context.Context) { <-ctx.Done(); done <- ctx.Err() })
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout missing")
	}
}
