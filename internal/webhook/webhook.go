// Package webhook sends panel events as POST requests with JSON to the configured addresses.
// The secret is sent in the X-Webhook-Secret header. The queue is in memory: when the panel stops,
// unsent events are lost, which is enough for a personal panel.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Default values.
const (
	QueueSize      = 256
	RequestTimeout = 10 * time.Second
)

// DefaultRetries are the pauses before retries: 1 + len attempts in total.
var DefaultRetries = []time.Duration{time.Second, 5 * time.Second, 30 * time.Second}

// Sender delivers events. Create it with New and start it with Run.
type Sender struct {
	urls    []string
	secret  string
	client  *http.Client
	retries []time.Duration
	queue   chan []byte
	log     *slog.Logger
}

// New prepares a sender to urls. An empty list means events are dropped.
func New(urls []string, secret string, log *slog.Logger) *Sender {
	if log == nil {
		log = slog.Default()
	}
	return &Sender{
		urls: urls, secret: secret, retries: DefaultRetries, log: log,
		client: &http.Client{Timeout: RequestTimeout},
		queue:  make(chan []byte, QueueSize),
	}
}

// Send queues an event (any JSON-serializable value) without blocking.
func (s *Sender) Send(event any) {
	if len(s.urls) == 0 {
		return
	}
	body, err := json.Marshal(event)
	if err != nil {
		s.log.Error("webhook: marshal event", "err", err)
		return
	}
	select {
	case s.queue <- body:
	default:
		s.log.Warn("webhook: queue is full, event dropped", "event", string(body))
	}
}

// Run sends events until ctx is cancelled.
func (s *Sender) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case body := <-s.queue:
			for _, u := range s.urls {
				s.deliver(ctx, u, body)
			}
		}
	}
}

func (s *Sender) deliver(ctx context.Context, url string, body []byte) {
	var err error
	for attempt := 0; ; attempt++ {
		if err = s.post(ctx, url, body); err == nil {
			return
		}
		if attempt >= len(s.retries) {
			break
		}
		select {
		case <-time.After(s.retries[attempt]):
		case <-ctx.Done():
			return
		}
	}
	s.log.Error("webhook: delivery failed", "url", url, "err", err)
}

func (s *Sender) post(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "giocondo-vpn-webhook")
	if s.secret != "" {
		req.Header.Set("X-Webhook-Secret", s.secret)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
