package nodeconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/artemlavr/giocondo/internal/service/singbox"
)

// Prepare builds the desired configuration; Sync and reconciliation only deliver
// and record its result. Missing WireGuard peers are issued through alloc, which serializes them with
// monitor creation.
func Prepare(
	ctx context.Context,
	alloc *Allocator,
	nodeSource NodeSource,
	userSource UserSource,
	nodeID int64,
	at time.Time,
	options singbox.Options,
) ([]byte, string, error) {
	node, users, err := Input(ctx, nodeSource, userSource, nodeID, at)
	if err != nil {
		return nil, "", err
	}

	if wg := node.WireGuardInbound(); wg != nil && wg.WireGuard != nil {
		if err := alloc.EnsureWGPeers(ctx, nodeSource, userSource, wg.WireGuard.WGSubnet()); err != nil {
			return nil, "", fmt.Errorf("wg peers: %w", err)
		}

		if node, users, err = Input(ctx, nodeSource, userSource, nodeID, at); err != nil {
			return nil, "", err
		}
	}

	cfg, err := singbox.Build(node, users, options)
	if err != nil {
		return nil, "", err
	}

	data, err := cfg.Marshal()
	if err != nil {
		return nil, "", err
	}

	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	return data, hash, nil
}
