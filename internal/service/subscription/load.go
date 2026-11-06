package subscription

import (
	"context"
	"fmt"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// Source supplies the stored state a subscription profile is built from.
type Source struct {
	Users    UserSource
	Nodes    NodeSource
	Hosts    HostSource
	Settings SettingsSource
}

// UserSource reads the subscription owner and their usage.
type UserSource interface {
	GetUserByToken(ctx context.Context, token string) (*panel.User, error)
	UserUsage(ctx context.Context, userID int64) (panel.Usage, error)
}

// NodeSource reads the nodes and inbounds offered in a subscription.
type NodeSource interface {
	ListNodes(ctx context.Context) ([]panel.Node, error)
	ListInbounds(ctx context.Context, nodeID int64) ([]panel.Inbound, error)
}

// HostSource reads inbound host overrides.
type HostSource interface {
	ListHosts(ctx context.Context) ([]panel.Host, error)
}

// SettingsSource reads the subscription settings.
type SettingsSource interface {
	SubscriptionSettings(ctx context.Context) (panel.SubscriptionSettings, error)
}

// LoadProfile loads a subscription profile.
func LoadProfile(ctx context.Context, src Source, token string) (*Profile, error) {
	u, err := src.Users.GetUserByToken(ctx, token)
	if err != nil {
		return nil, err
	}

	settings, err := src.Settings.SubscriptionSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}

	usage, err := src.Users.UserUsage(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("usage: %w", err)
	}

	nodes, err := src.Nodes.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("nodes: %w", err)
	}

	hosts, err := src.Hosts.ListHosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("hosts: %w", err)
	}

	byInbound := map[int64][]panel.Host{}
	for _, h := range hosts {
		byInbound[h.InboundID] = append(byInbound[h.InboundID], h)
	}

	p := &Profile{User: *u, Usage: usage, Settings: settings}

	for _, n := range nodes {
		if n.Status == panel.NodeDisabled {
			continue
		}

		ins, err := src.Nodes.ListInbounds(ctx, n.ID)
		if err != nil {
			return nil, fmt.Errorf("inbounds of node %s: %w", n.Name, err)
		}

		np := NodeProfile{Node: n, Hosts: map[string][]panel.Host{}}
		for _, in := range ins {
			np.Inbounds = append(np.Inbounds, in.Spec)
			if hs := byInbound[in.ID]; len(hs) > 0 {
				np.Hosts[in.Spec.EffectiveTag()] = hs
			}
		}

		p.Nodes = append(p.Nodes, np)
	}

	return p, nil
}
