package nodeconfig

import (
	"context"
	"errors"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/singbox"
)

// MonitorStore stores node monitor credentials, which are kept apart from public users.
type MonitorStore interface {
	NodeSource
	SaveMonitorCredentials(context.Context, int64, protocol.User) error
}

// EnsureMonitor returns the credentials of the node monitor user, creating them on first use; the
// bool reports whether they are new.
func (a *Allocator) EnsureMonitor(
	ctx context.Context,
	nodeSource MonitorStore,
	userSource UserSource,
	nodeID int64,
) (*protocol.User, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	existing, err := nodeSource.MonitorCredentials(ctx, nodeID)
	if err == nil {
		return existing, false, nil
	}

	if !errors.Is(err, panel.ErrNotFound) {
		return nil, false, err
	}

	uuid, err := protocol.NewUUID()
	if err != nil {
		return nil, false, err
	}

	u := protocol.User{Name: protocol.MonitorUserName(nodeID), UUID: uuid}

	ins, err := nodeSource.ListInbounds(ctx, nodeID)
	if err != nil {
		return nil, false, err
	}

	for _, in := range ins {
		if in.Spec.Type != protocol.TypeWireGuard {
			continue
		}

		if u.WG, err = newMonitorPeer(ctx, nodeSource, userSource, in.Spec.WireGuard.WGSubnet()); err != nil {
			return nil, false, err
		}
	}

	if err = nodeSource.SaveMonitorCredentials(ctx, nodeID, u); err != nil {
		return nil, false, err
	}

	return &u, true, nil
}

// newMonitorPeer issues WireGuard keys and an address not taken by users or other node monitors.
func newMonitorPeer(
	ctx context.Context,
	nodeSource NodeSource,
	userSource UserSource,
	subnet string,
) (*protocol.WGPeer, error) {
	taken, err := takenWGAddresses(ctx, nodeSource, userSource)
	if err != nil {
		return nil, err
	}

	addr, err := singbox.AllocateWGAddress(subnet, taken)
	if err != nil {
		return nil, err
	}

	priv, pub, err := protocol.NewWGKeypair()
	if err != nil {
		return nil, err
	}

	psk, err := protocol.NewPresharedKey()
	if err != nil {
		return nil, err
	}

	return &protocol.WGPeer{Address: addr, PrivateKey: priv, PublicKey: pub, PresharedKey: psk}, nil
}

func takenWGAddresses(ctx context.Context, nodeSource NodeSource, userSource UserSource) ([]string, error) {
	users, err := userSource.ListUsers(ctx)
	if err != nil {
		return nil, err
	}

	taken := []string{}

	for _, other := range users {
		if other.WG != nil {
			taken = append(taken, other.WG.Address)
		}
	}

	nodes, err := nodeSource.ListNodes(ctx)
	if err != nil {
		return nil, err
	}

	for _, n := range nodes {
		m, e := nodeSource.MonitorCredentials(ctx, n.ID)
		if e == nil && m.WG != nil {
			taken = append(taken, m.WG.Address)
		} else if e != nil && !errors.Is(e, panel.ErrNotFound) {
			return nil, e
		}
	}

	return taken, nil
}
