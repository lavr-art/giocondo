// Package events sends panel events as POST requests with JSON to the configured addresses.
// The secret is sent in the X-Webhook-Secret header. The queue is in memory: when the panel stops,
// unsent events are lost, which is enough for a personal panel.
package events

import (
	"context"
	"encoding/json"
	"time"

	"github.com/artemlavr/giocondo/pkg/logger"
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
	urls     []string
	delivery Delivery
	retries  []time.Duration
	queue    chan []byte
}

// New prepares a sender to urls. An empty list means events are dropped.
func New(urls []string, delivery Delivery) *Sender {
	return &Sender{
		urls: urls, delivery: delivery, retries: DefaultRetries,
		queue: make(chan []byte, QueueSize),
	}
}

// Send queues an event (any JSON-serializable value) without blocking. It is called from the
// services' Emit hook, which carries no request context.
func (s *Sender) Send(event any) {
	if len(s.urls) == 0 {
		return
	}

	body, err := json.Marshal(event)
	if err != nil {
		logger.FromCtx(context.Background()).Error("webhook: marshal event", "err", err)

		return
	}

	select {
	case s.queue <- body:
	default:
		logger.FromCtx(context.Background()).Warn("webhook: queue is full, event dropped", "event", string(body))
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
		if err = s.delivery.Post(ctx, url, body); err == nil {
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

	logger.FromCtx(ctx).Error("webhook: delivery failed", "url", url, "err", err)
}

// Delivery describes how a message is delivered.
type Delivery interface {
	Post(context.Context, string, []byte) error
}
