package nodes

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// PreviewDNS previews DNS changes.
func (s *Reader) PreviewDNS(ctx context.Context, id int64) (panel.DNSPreview, error) {
	n, err := s.nodes.GetNode(ctx, id)
	if err != nil {
		return panel.DNSPreview{}, err
	}

	domain := strings.TrimSuffix(strings.ToLower(s.dnsDomain), ".")
	if !s.dnsEnabled || domain == "" || !panel.DomainPattern.MatchString(domain) ||
		len(domain) > 230 || net.ParseIP(domain) != nil {
		return panel.DNSPreview{}, operation.Invalid("managed DNS is not configured")
	}

	if _, err = panel.ParseIPv4(n.IP); err != nil {
		return panel.DNSPreview{}, err
	}

	return panel.DNSPreview{
		Host:        fmt.Sprintf("n%d.%s", n.ID, domain),
		IP:          n.IP,
		CurrentHost: n.Host,
		Managed:     n.ManagedDNS,
	}, nil
}
