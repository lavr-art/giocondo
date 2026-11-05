package network

import (
	"context"
	"net"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/internal/service/singbox"
)

// EnableDNS turns on managed DNS for a node.
func (s *Service) EnableDNS(ctx context.Context, id int64, confirm bool) (panel.NodeView, error) {
	s.nodeLock.Lock()
	defer s.nodeLock.Unlock()

	if err := s.Unlocked(ctx, id); err != nil {
		return panel.NodeView{}, err
	}

	preview, err := s.queries.PreviewDNS(ctx, id)
	if err != nil {
		return panel.NodeView{}, err
	}

	if !confirm {
		return panel.NodeView{}, operation.Invalid("confirm DNS migration after reviewing preview")
	}

	if err = s.checkNoIPHosts(ctx, id); err != nil {
		return panel.NodeView{}, err
	}

	n, err := s.nodes.GetNode(ctx, id)
	if err != nil {
		return panel.NodeView{}, err
	}

	if n.ManagedDNS && n.Host != preview.Host {
		return panel.NodeView{}, operation.Invalid("managed domain changed; restore PANEL_DNS_DOMAIN")
	}

	if err = s.ensureInstallation(ctx, n); err != nil {
		return panel.NodeView{}, err
	}

	record, err := s.ensureOwnedRecord(ctx, n, preview.Host)
	if err != nil {
		return panel.NodeView{}, err
	}

	if n.Host != preview.Host {
		if err = s.reissueSelfSigned(ctx, id, preview.Host); err != nil {
			return panel.NodeView{}, err
		}
	}

	if err = s.network.SetManagedDNS(ctx, id, preview.Host, record.ID); err != nil {
		return panel.NodeView{}, err
	}

	s.hooks.Kick()

	return s.queries.GetNode(ctx, id)
}

// checkNoIPHosts refuses managed DNS while a host override points at a bare IP that DNS would not follow.
func (s *Service) checkNoIPHosts(ctx context.Context, id int64) error {
	hosts, err := s.hosts.ListHosts(ctx, id)
	if err != nil {
		return err
	}

	for _, h := range hosts {
		if net.ParseIP(h.Address) != nil {
			return operation.Invalid("replace IP host overrides before enabling managed DNS")
		}
	}

	return nil
}

// ensureOwnedRecord creates or updates the node record but never takes over a record owned by someone else.
func (s *Service) ensureOwnedRecord(
	ctx context.Context,
	n *panel.Node,
	host string,
) (panel.DNSRecord, error) {
	rs, err := s.dns.Lookup(ctx, host)
	if err != nil {
		return panel.DNSRecord{}, err
	}

	if len(rs) > 0 && (!n.ManagedDNS || len(rs) != 1 || rs[0].ID != n.DNSRecordID) {
		return panel.DNSRecord{}, operation.Invalid("DNS record already exists and is not owned by this node")
	}

	return s.dns.Ensure(ctx, host, n.IP, n.DNSRecordID)
}

// reissueSelfSigned issues self-signed Hysteria2 certificates for the new host name.
func (s *Service) reissueSelfSigned(ctx context.Context, id int64, host string) error {
	ins, err := s.nodes.ListInbounds(ctx, id)
	if err != nil {
		return err
	}

	for _, ib := range ins {
		if ib.Spec.Hysteria2 == nil || ib.Spec.Hysteria2.TLSMode() != protocol.TLSSelfSigned {
			continue
		}

		cert, key, err := singbox.SelfSignedCert(host)
		if err != nil {
			return err
		}

		ib.Spec.Hysteria2.CertPEM, ib.Spec.Hysteria2.KeyPEM = cert, key
		if err = s.nodes.UpdateInbound(ctx, &ib); err != nil {
			return err
		}
	}

	return nil
}
