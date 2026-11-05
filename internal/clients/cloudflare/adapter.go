package cloudflare

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// DNS is a DNS provider abstraction.
type DNS struct{ Manager Manager }

func record(r Record) panel.DNSRecord {
	return panel.DNSRecord{ID: r.ID, Type: r.Type, Name: r.Name, Content: r.Content, TTL: r.TTL, Proxied: r.Proxied}
}

// Ensure creates the resource if it is missing.
func (d DNS) Ensure(ctx context.Context, h, ip, id string) (panel.DNSRecord, error) {
	r, err := d.Manager.Ensure(ctx, h, ip, id)

	return record(r), err
}

// Lookup resolves a DNS record.
func (d DNS) Lookup(ctx context.Context, h string) ([]panel.DNSRecord, error) {
	rs, err := d.Manager.Lookup(ctx, h)
	if err != nil {
		return nil, err
	}

	out := make([]panel.DNSRecord, len(rs))
	for i, r := range rs {
		out[i] = record(r)
	}

	return out, nil
}

// Delete removes an entity.
func (d DNS) Delete(ctx context.Context, id string) error { return d.Manager.Delete(ctx, id) }
