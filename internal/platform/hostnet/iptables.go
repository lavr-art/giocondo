// Package hostnet applies the panel network command to the host: it owns only the VPNCTL_UDP
// iptables chain that maps UDP alias addresses to the primary IP.
package hostnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
)

const (
	chain        = "VPNCTL_UDP"
	applyTimeout = 10 * time.Second
)

// ApplyNetwork replaces the VPNCTL_UDP rules with DNAT rules for c and hooks the chain into nat
// PREROUTING once. Existing firewall rules and host addresses are never changed.
func ApplyNetwork(ctx context.Context, c *nodesync.NetworkConfig) error {
	assigned, err := localAddresses()
	if err != nil {
		return err
	}

	rules, err := networkRules(c, assigned)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, applyTimeout)
	defer cancel()

	restore := exec.CommandContext(ctx, "iptables-restore", "--noflush", "--wait", "5")

	restore.Stdin = strings.NewReader(rules)
	if err := restore.Run(); err != nil {
		return fmt.Errorf("UDP alias rules: %w", err)
	}

	if exec.CommandContext(ctx, "iptables", "-w", "5", "-t", "nat", "-C", "PREROUTING", "-j", chain).Run() == nil {
		return nil
	}

	hook := exec.CommandContext(ctx, "iptables", "-w", "5", "-t", "nat", "-A", "PREROUTING", "-j", chain)
	if err := hook.Run(); err != nil {
		return fmt.Errorf("UDP alias chain: %w", err)
	}

	return nil
}

func localAddresses() (map[string]bool, error) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	assigned := map[string]bool{}

	for _, a := range addresses {
		if prefix, err := netip.ParsePrefix(a.String()); err == nil {
			assigned[prefix.Addr().Unmap().String()] = true
		}
	}

	return assigned, nil
}

// networkRules renders the iptables-restore input for c. assigned holds the addresses configured on
// the host: with provider-side NAT the public addresses are not local and no rules are needed.
func networkRules(c *nodesync.NetworkConfig, assigned map[string]bool) (string, error) {
	primary, err := netip.ParseAddr(c.PrimaryIP)
	if err != nil || !primary.Is4() || primary.IsUnspecified() {
		return "", errors.New("invalid canonical IPv4")
	}

	for _, port := range c.UDPPorts {
		if port == 0 || port > 65535 {
			return "", errors.New("invalid UDP port")
		}
	}

	aliases := make([]netip.Addr, 0, len(c.Aliases))
	for _, raw := range c.Aliases {
		addr, err := netip.ParseAddr(raw)
		if err != nil || !addr.Is4() || addr.IsUnspecified() || addr.IsMulticast() {
			return "", errors.New("invalid alias IPv4")
		}

		aliases = append(aliases, addr)
	}

	sort.Slice(aliases, func(i, j int) bool { return aliases[i].Less(aliases[j]) })

	var out strings.Builder
	fmt.Fprintf(&out, "*nat\n:%s - [0:0]\n-F %s\n", chain, chain)

	for _, addr := range aliases {
		if addr == primary || !assigned[primary.String()] || !assigned[addr.String()] {
			continue
		}

		for _, port := range c.UDPPorts {
			fmt.Fprintf(&out, "-A %s -d %s -p udp --dport %d -j DNAT --to-destination %s\n", chain, addr, port, primary)
		}
	}

	out.WriteString("COMMIT\n")

	return out.String(), nil
}
