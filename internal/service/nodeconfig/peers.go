package nodeconfig

import (
	"context"
	"errors"
	"sync"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/singbox"
)

// Allocator reserves WireGuard addresses for users and node monitors, which share one subnet. Its
// methods run one at a time, so concurrent config preparation and monitor creation cannot hand out an
// address twice. The panel uses a single Allocator; the zero value is ready to use.
type Allocator struct{ mu sync.Mutex }

// EnsureWGPeers issues WireGuard keys and an address from subnet to all users that lack them
// and stores them. A user has one address on all nodes, so WG nodes must share the subnet.
func (a *Allocator) EnsureWGPeers(
	ctx context.Context,
	nodeSource NodeSource,
	userSource UserSource,
	subnet string,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	users, err := userSource.ListUsers(ctx)
	if err != nil {
		return err
	}

	var taken []string

	for _, u := range users {
		if u.WG != nil && u.WG.Address != "" {
			taken = append(taken, u.WG.Address)
		}
	}

	nodes, err := nodeSource.ListNodes(ctx)
	if err != nil {
		return err
	}

	for _, n := range nodes {
		m, e := nodeSource.MonitorCredentials(ctx, n.ID)
		if e == nil && m.WG != nil {
			taken = append(taken, m.WG.Address)
		} else if e != nil && !errors.Is(e, panel.ErrNotFound) {
			return e
		}
	}

	for i := range users {
		u := &users[i]
		if u.WG != nil && u.WG.PublicKey != "" && u.WG.Address != "" {
			continue
		}

		priv, pub, err := protocol.NewWGKeypair()
		if err != nil {
			return err
		}

		psk, err := protocol.NewPresharedKey()
		if err != nil {
			return err
		}

		addr, err := singbox.AllocateWGAddress(subnet, taken)
		if err != nil {
			return err
		}

		peer := &protocol.WGPeer{PrivateKey: priv, PublicKey: pub, PresharedKey: psk, Address: addr}
		if err := userSource.SetUserWG(ctx, u.ID, peer); err != nil {
			return err
		}

		taken = append(taken, addr)
	}

	return nil
}
