package webhook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu     sync.Mutex
	bodies []string
	fails  int // how many first requests answer 500
	got    chan struct{}
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req.Header.Get("X-Webhook-Secret") != "s3" || req.Header.Get("Content-Type") != "application/json" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if r.fails > 0 {
		r.fails--
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	b, _ := io.ReadAll(req.Body)
	r.bodies = append(r.bodies, string(b))
	r.got <- struct{}{}
}

func TestDeliverWithRetry(t *testing.T) {
	rec := &recorder{fails: 2, got: make(chan struct{}, 10)}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	s := New([]string{srv.URL}, "s3", nil)
	s.retries = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	s.Send(map[string]string{"event": "user_created"})
	select {
	case <-rec.got:
	case <-time.After(5 * time.Second):
		t.Fatal("event not delivered")
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.bodies) != 1 || rec.bodies[0] != `{"event":"user_created"}` {
		t.Errorf("bodies %q", rec.bodies)
	}
}

func TestGiveUpAndNoURLs(t *testing.T) {
	rec := &recorder{fails: 100, got: make(chan struct{}, 10)}
	srv := httptest.NewServer(rec)
	defer srv.Close()
	s := New([]string{srv.URL}, "s3", nil)
	s.retries = []time.Duration{time.Millisecond}
	s.deliver(context.Background(), srv.URL, []byte(`{}`))
	rec.mu.Lock()
	if rec.fails != 98 {
		t.Errorf("attempts = %d, want 2", 100-rec.fails)
	}
	rec.mu.Unlock()

	off := New(nil, "", nil)
	off.Send("x") // without addresses nothing happens silently
	if len(off.queue) != 0 {
		t.Error("event queued without URLs")
	}
}

func TestQueueFullDrops(t *testing.T) {
	s := New([]string{"http://127.0.0.1:1"}, "", nil)
	for range QueueSize + 10 {
		s.Send("x")
	}
	if len(s.queue) != QueueSize {
		t.Errorf("queue len %d", len(s.queue))
	}
}
