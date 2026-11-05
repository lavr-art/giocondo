// Package dnsresolver resolves node hostnames.
package dnsresolver

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
)

// ResolveAuthoritative verifies the A response against the domain's authoritative DNS.
func ResolveAuthoritative(ctx context.Context, host, ip string) error {
	labels := strings.Split(host, ".")
	for i := range len(labels) - 1 {
		servers, err := net.DefaultResolver.LookupNS(ctx, strings.Join(labels[i:], "."))
		if err != nil || len(servers) == 0 {
			continue
		}

		for _, server := range servers {
			resolver := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(server.Host, "53"))
			}}

			addresses, err := resolver.LookupHost(ctx, host)
			if err != nil {
				continue
			}

			if slices.Contains(addresses, ip) {
				return nil
			}
		}

		return errors.New("authoritative DNS does not return the new IP")
	}

	return errors.New("authoritative nameservers not found")
}
