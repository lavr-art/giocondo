package nodehub

import (
	"context"
	"errors"

	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
)

// store.WGAllocationMu serializes address allocation: two streams must not give one address to different people
// (UNIQUE(wg_address) in the database would prevent it anyway, but the config build would then fail).

// EnsureWGPeers issues WireGuard keys and an address from subnet to all users that lack them
// and stores them. A user has one address on all nodes, so WG nodes must share the subnet.
func EnsureWGPeers(ctx context.Context, st store.Store, subnet string) error {
	store.WGAllocationMu.Lock()
	defer store.WGAllocationMu.Unlock()
	users, err := st.ListUsers(ctx)
	if err != nil {
		return err
	}
	var taken []string
	for _, u := range users {
		if u.WG != nil && u.WG.Address != "" {
			taken = append(taken, u.WG.Address)
		}
	}
	nodes, err := st.ListNodes(ctx)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		m, e := st.MonitorCredentials(ctx, n.ID)
		if e == nil && m.WG != nil {
			taken = append(taken, m.WG.Address)
		} else if e != nil && !errors.Is(e, store.ErrNotFound) {
			return e
		}
	}
	for i := range users {
		u := &users[i]
		if u.WG != nil && u.WG.PublicKey != "" && u.WG.Address != "" {
			continue
		}
		priv, pub, err := singbox.NewWGKeypair()
		if err != nil {
			return err
		}
		psk, err := singbox.NewPresharedKey()
		if err != nil {
			return err
		}
		addr, err := singbox.AllocateWGAddress(subnet, taken)
		if err != nil {
			return err
		}
		u.WG = &singbox.WGPeer{PrivateKey: priv, PublicKey: pub, PresharedKey: psk, Address: addr}
		if err := st.UpdateUser(ctx, u); err != nil {
			return err
		}
		taken = append(taken, addr)
	}
	return nil
}
