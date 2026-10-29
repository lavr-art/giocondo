package store

import (
	"context"
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/singbox"
)

// SingboxUser converts a user into the config generator model.
func (u User) SingboxUser() singbox.User {
	out := singbox.User{Name: u.Name, UUID: u.UUID}
	if u.WG != nil {
		wg := *u.WG
		out.WG = &wg
	}
	return out
}

// SingboxNode converts a node and its protocols into the generator model. The node ID in the config is its name.
func SingboxNode(n Node, inbounds []Inbound) singbox.Node {
	out := singbox.Node{ID: n.Name, Host: n.Host}
	for _, in := range inbounds {
		out.Inbounds = append(out.Inbounds, in.Spec)
	}
	return out
}

// NodeConfigInput builds the input for singbox.Build: the node protocols and the users
// active at at. Disabled users and users over the limit are left out of the node config.
func NodeConfigInput(ctx context.Context, s Store, nodeID int64, at time.Time) (*singbox.Node, []singbox.User, error) {
	n, err := s.GetNode(ctx, nodeID)
	if err != nil {
		return nil, nil, err
	}
	inbounds, err := s.ListInbounds(ctx, nodeID)
	if err != nil {
		return nil, nil, err
	}
	active, err := s.ActiveUsers(ctx, at)
	if err != nil {
		return nil, nil, err
	}
	node := SingboxNode(*n, inbounds)
	users := make([]singbox.User, 0, len(active))
	for _, u := range active {
		users = append(users, u.SingboxUser())
	}
	monitor, err := s.MonitorCredentials(ctx, nodeID)
	if err == nil {
		users = append(users, *monitor)
	} else if !errors.Is(err, ErrNotFound) {
		return nil, nil, err
	}
	return &node, users, nil
}
