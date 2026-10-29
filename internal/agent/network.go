package agent

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

	"github.com/artemlavr/giocondo/internal/nodeapi/pb"
)

// networkRules owns only VPNCTL_UDP, never existing firewall rules or addresses.
func networkRules(c *pb.NetworkConfig, assigned map[string]bool) (string, error) {
	primary, err := netip.ParseAddr(c.PrimaryIp)
	if err != nil || !primary.Is4() || primary.IsUnspecified() {
		return "", errors.New("invalid canonical IPv4")
	}
	aliases := append([]string(nil), c.Aliases...)
	sort.Strings(aliases)
	out := "*nat\n:VPNCTL_UDP - [0:0]\n-F VPNCTL_UDP\n"
	for _, raw := range aliases {
		addr, err := netip.ParseAddr(raw)
		if err != nil || !addr.Is4() || addr.IsUnspecified() || addr.IsMulticast() {
			return "", errors.New("invalid alias IPv4")
		}
		for _, port := range c.UdpPorts {
			if port == 0 || port > 65535 {
				return "", errors.New("invalid UDP port")
			}
		}
		// With provider-side NAT none of the public addresses are local; no rules are needed.
		if addr == primary || !assigned[primary.String()] || !assigned[addr.String()] {
			continue
		}
		for _, port := range c.UdpPorts {
			out += fmt.Sprintf("-A VPNCTL_UDP -d %s -p udp --dport %d -j DNAT --to-destination %s\n", addr, port, primary)
		}
	}
	return out + "COMMIT\n", nil
}
func ApplyNetwork(ctx context.Context, c *pb.NetworkConfig) error {
	assigned := map[string]bool{}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return err
	}
	for _, a := range addresses {
		prefix, e := netip.ParsePrefix(a.String())
		if e == nil {
			assigned[prefix.Addr().Unmap().String()] = true
		}
	}
	rules, err := networkRules(c, assigned)
	if err != nil {
		return err
	}
	work, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	restore := exec.CommandContext(work, "iptables-restore", "--noflush", "--wait", "5")
	restore.Stdin = strings.NewReader(rules)
	if err = restore.Run(); err != nil {
		return fmt.Errorf("UDP alias rules: %w", err)
	}
	if err = exec.CommandContext(work, "iptables", "-w", "5", "-t", "nat", "-C", "PREROUTING", "-j", "VPNCTL_UDP").Run(); err != nil {
		if err = exec.CommandContext(work, "iptables", "-w", "5", "-t", "nat", "-A", "PREROUTING", "-j", "VPNCTL_UDP").Run(); err != nil {
			return fmt.Errorf("UDP alias chain: %w", err)
		}
	}
	return nil
}
