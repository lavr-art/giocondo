package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Record is a DNS-only A record. TTL is always 60 seconds.
type Record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

type Manager interface {
	Ensure(context.Context, string, string, string) (Record, error)
	Lookup(context.Context, string) ([]Record, error)
	Delete(context.Context, string) error
}

type Cloudflare struct {
	Token, ZoneID string
	BaseURL       string
	Client        *http.Client
}

func (c *Cloudflare) request(ctx context.Context, method, path string, body any, out any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+"/zones/"+url.PathEscape(c.ZoneID)+"/dns_records"+path, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("cloudflare request: %w", err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		_ = res.Body.Close()
		if readErr != nil {
			return readErr
		}
		// Never retry ambiguous writes: callers reconcile their journal against DNS.
		if method == http.MethodGet && (res.StatusCode == 429 || res.StatusCode >= 500) && attempt < 2 {
			timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		var env struct {
			Success bool            `json:"success"`
			Result  json.RawMessage `json:"result"`
			Errors  []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err = json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("cloudflare HTTP %d: invalid response", res.StatusCode)
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 || !env.Success {
			return fmt.Errorf("cloudflare HTTP %d: request rejected", res.StatusCode)
		}
		if out != nil {
			return json.Unmarshal(env.Result, out)
		}
		return nil
	}
	return errors.New("cloudflare retries exhausted")
}
func (c *Cloudflare) Lookup(ctx context.Context, name string) ([]Record, error) {
	var rs []Record
	err := c.request(ctx, "GET", "?name="+url.QueryEscape(name), nil, &rs)
	return rs, err
}
func (c *Cloudflare) Ensure(ctx context.Context, name, ip, id string) (Record, error) {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() {
		return Record{}, errors.New("DNS A record needs IPv4")
	}
	rs, err := c.Lookup(ctx, name)
	if err != nil {
		return Record{}, err
	}
	if len(rs) > 1 {
		return Record{}, errors.New("conflicting DNS records")
	}
	if len(rs) == 1 && (id == "" || rs[0].ID != id || rs[0].Type != "A") {
		return Record{}, errors.New("DNS record is not owned by this node")
	}
	r := Record{Type: "A", Name: name, Content: addr.String(), TTL: 60}
	// A missing previously-owned record may be recreated, but a new ID must be persisted.
	path, method := "", "POST"
	if len(rs) == 1 {
		path, method = "/"+url.PathEscape(id), "PATCH"
	}
	var result Record
	err = c.request(ctx, method, path, r, &result)
	if err == nil && (result.ID == "" || result.Name != name || result.Content != ip || result.TTL != 60 || result.Proxied || result.Type != "A") {
		err = errors.New("cloudflare returned an unexpected record")
	}
	return result, err
}
func (c *Cloudflare) Delete(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("record ID required")
	}
	return c.request(ctx, "DELETE", "/"+url.PathEscape(id), nil, nil)
}
