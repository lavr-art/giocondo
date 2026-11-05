// Package webhook delivers panel events over HTTP.
package webhook

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RequestTimeout is the default outbound request timeout.
const RequestTimeout = 10 * time.Second

// Client talks to a remote service.
type Client struct {
	Secret string
	HTTP   *http.Client
}

// New returns a client that sends secret in X-Webhook-Secret and limits each request to RequestTimeout.
func New(secret string) *Client {
	return &Client{Secret: secret, HTTP: &http.Client{Timeout: RequestTimeout}}
}

// Post sends a message to a webhook.
func (s *Client) Post(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "giocondo-vpn-webhook")

	if s.Secret != "" {
		req.Header.Set("X-Webhook-Secret", s.Secret)
	}

	resp, err := s.HTTP.Do(req)
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
