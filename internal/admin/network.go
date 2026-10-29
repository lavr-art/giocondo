package admin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
)

type DNSPreview struct {
	Host        string `json:"host"`
	IP          string `json:"ip"`
	CurrentHost string `json:"current_host"`
	Managed     bool   `json:"managed"`
}

func (s *Service) PreviewDNS(ctx context.Context, id int64) (DNSPreview, error) {
	n, err := s.Store.GetNode(ctx, id)
	if err != nil {
		return DNSPreview{}, err
	}
	domain := strings.TrimSuffix(strings.ToLower(s.DNSDomain), ".")
	if s.DNS == nil || domain == "" || !domainRe.MatchString(domain) || len(domain) > 230 || net.ParseIP(domain) != nil {
		return DNSPreview{}, invalid("managed DNS is not configured")
	}
	if _, err = parseIPv4(n.IP); err != nil {
		return DNSPreview{}, err
	}
	return DNSPreview{Host: fmt.Sprintf("n%d.%s", n.ID, domain), IP: n.IP, CurrentHost: n.Host, Managed: n.ManagedDNS}, nil
}
func (s *Service) EnableDNS(ctx context.Context, id int64, confirm bool) (NodeView, error) {
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	if err := s.unlocked(ctx, id); err != nil {
		return NodeView{}, err
	}
	preview, err := s.PreviewDNS(ctx, id)
	if err != nil {
		return NodeView{}, err
	}
	if !confirm {
		return NodeView{}, invalid("confirm DNS migration after reviewing preview")
	}
	hosts, err := s.ListHosts(ctx, id)
	if err != nil {
		return NodeView{}, err
	}
	for _, h := range hosts {
		if net.ParseIP(h.Address) != nil {
			return NodeView{}, invalid("replace IP host overrides before enabling managed DNS")
		}
	}
	n, err := s.Store.GetNode(ctx, id)
	if err != nil {
		return NodeView{}, err
	}
	if n.ManagedDNS && n.Host != preview.Host {
		return NodeView{}, invalid("managed domain changed; restore PANEL_DNS_DOMAIN")
	}
	if err = s.ensureInstallation(ctx, n); err != nil {
		return NodeView{}, err
	}
	rs, err := s.DNS.Lookup(ctx, preview.Host)
	if err != nil {
		return NodeView{}, err
	}
	if len(rs) > 0 && (!n.ManagedDNS || len(rs) != 1 || rs[0].ID != n.DNSRecordID) {
		return NodeView{}, invalid("DNS record already exists and is not owned by this node")
	}
	record, err := s.DNS.Ensure(ctx, preview.Host, n.IP, n.DNSRecordID)
	if err != nil {
		return NodeView{}, err
	}
	if n.Host != preview.Host {
		ins, e := s.Store.ListInbounds(ctx, id)
		if e != nil {
			return NodeView{}, e
		}
		for _, ib := range ins {
			if ib.Spec.Hysteria2 != nil && ib.Spec.Hysteria2.TLSMode() == singbox.TLSSelfSigned {
				cert, key, e := singbox.SelfSignedCert(preview.Host)
				if e != nil {
					return NodeView{}, e
				}
				ib.Spec.Hysteria2.CertPEM = cert
				ib.Spec.Hysteria2.KeyPEM = key
				if e = s.Store.UpdateInbound(ctx, &ib); e != nil {
					return NodeView{}, e
				}
			}
		}
	}
	if err = s.Store.SetManagedDNS(ctx, id, preview.Host, record.ID); err != nil {
		return NodeView{}, err
	}
	s.kick()
	return s.GetNode(ctx, id)
}
func parseIPv4(raw string) (string, error) {
	ip, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLoopback() {
		return "", invalid("a unicast non-loopback IPv4 address is required")
	}
	return ip.String(), nil
}
func (s *Service) unlocked(ctx context.Context, id int64) error {
	locked, err := s.Store.NetworkLocked(ctx, id)
	if err != nil {
		return err
	}
	if locked {
		return store.ErrConflict
	}
	return nil
}
func (s *Service) ensureInstallation(ctx context.Context, n *store.Node) error {
	if n.ActiveInstallation != 0 {
		return nil
	}
	i, err := s.Store.InstallationByIdentity(ctx, n.Name)
	if errors.Is(err, store.ErrNotFound) {
		i = &store.Installation{NodeID: n.ID, Identity: n.Name, IP: n.IP, State: "active"}
		err = s.Store.CreateInstallation(ctx, i)
	}
	if err != nil {
		return err
	}
	if err = s.Store.ActivateInstallation(ctx, n.ID, i.ID, n.IP); err != nil {
		return err
	}
	n.ActiveInstallation = i.ID
	addresses, err := s.Store.ListAddresses(ctx, n.ID)
	if err != nil {
		return err
	}
	for _, a := range addresses {
		if a.IP == n.IP {
			return nil
		}
	}
	return s.Store.CreateAddress(ctx, &store.Address{NodeID: n.ID, InstallationID: i.ID, IP: n.IP})
}
func (s *Service) ListAddresses(ctx context.Context, id int64) ([]store.Address, error) {
	if _, err := s.Store.GetNode(ctx, id); err != nil {
		return nil, err
	}
	return s.Store.ListAddresses(ctx, id)
}
func (s *Service) AddAddress(ctx context.Context, id int64, raw string) (store.Address, error) {
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	if err := s.unlocked(ctx, id); err != nil {
		return store.Address{}, err
	}
	ip, err := parseIPv4(raw)
	if err != nil {
		return store.Address{}, err
	}
	n, err := s.Store.GetNode(ctx, id)
	if err != nil {
		return store.Address{}, err
	}
	if n.IP == "" {
		return store.Address{}, invalid("set initial node IP first")
	}
	if err = s.ensureInstallation(ctx, n); err != nil {
		return store.Address{}, err
	}
	a := store.Address{NodeID: id, InstallationID: n.ActiveInstallation, IP: ip}
	err = s.Store.CreateAddress(ctx, &a)
	if err == nil {
		s.kick()
	}
	return a, err
}
func (s *Service) DeleteAddress(ctx context.Context, id, addressID int64) error {
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	if err := s.unlocked(ctx, id); err != nil {
		return err
	}
	err := s.Store.DeleteAddress(ctx, id, addressID)
	if err == nil {
		s.kick()
	}
	return err
}
func operationID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
