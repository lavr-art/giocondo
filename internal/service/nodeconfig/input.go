package nodeconfig

import (
	"context"
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// User converts a user into the config generator model.
func User(u panel.User) protocol.User {
	out := protocol.User{Name: u.Name, UUID: u.UUID}
	if u.WG != nil {
		wg := *u.WG
		out.WG = &wg
	}

	return out
}

// Node converts a node and its protocols into the generator model. The node ID in the config is its name.
func Node(n panel.Node, inbounds []panel.Inbound) protocol.Node {
	out := protocol.Node{ID: n.Name, Host: n.Host}
	for _, in := range inbounds {
		out.Inbounds = append(out.Inbounds, in.Spec)
	}

	return out
}

// Input builds the input for singbox.Build: the node protocols and the users
// active at at. Disabled users and users over the limit are left out of the node config.
func Input(
	ctx context.Context,
	nodeSource NodeSource,
	userSource UserSource,
	nodeID int64,
	at time.Time,
) (*protocol.Node, []protocol.User, error) {
	n, err := nodeSource.GetNode(ctx, nodeID)
	if err != nil {
		return nil, nil, err
	}

	inbounds, err := nodeSource.ListInbounds(ctx, nodeID)
	if err != nil {
		return nil, nil, err
	}

	active, err := userSource.ActiveUsers(ctx, at)
	if err != nil {
		return nil, nil, err
	}

	node := Node(*n, inbounds)

	users := make([]protocol.User, 0, len(active))
	for _, u := range active {
		users = append(users, User(u))
	}

	monitor, err := nodeSource.MonitorCredentials(ctx, nodeID)
	if err == nil {
		users = append(users, *monitor)
	} else if !errors.Is(err, panel.ErrNotFound) {
		return nil, nil, err
	}

	return &node, users, nil
}

// NodeSource reads nodes, their inbounds and node monitor credentials.
type NodeSource interface {
	GetNode(context.Context, int64) (*panel.Node, error)
	ListNodes(context.Context) ([]panel.Node, error)
	ListInbounds(context.Context, int64) ([]panel.Inbound, error)
	MonitorCredentials(context.Context, int64) (*protocol.User, error)
}

// UserSource reads users and stores their WireGuard peers.
type UserSource interface {
	ActiveUsers(context.Context, time.Time) ([]panel.User, error)
	ListUsers(context.Context) ([]panel.User, error)
	SetUserWG(context.Context, int64, *protocol.WGPeer) error
}
