package admin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/healthcheck"
	"github.com/artemlavr/giocondo/internal/provision"
	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
)

type RotateIPInput struct {
	Mode      string    `json:"mode"`
	IP        string    `json:"ip"`
	SSH       *SSHInput `json:"ssh,omitempty"`
	SourceSSH *SSHInput `json:"source_ssh,omitempty"`
}

func (s *Service) RotateIP(ctx context.Context, id int64, in RotateIPInput) (store.NetworkOperation, error) {
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	n, err := s.Store.GetNode(ctx, id)
	if err != nil {
		return store.NetworkOperation{}, err
	}
	if !n.ManagedDNS || s.DNS == nil {
		return store.NetworkOperation{}, invalid("enable managed DNS first")
	}
	if s.Checker == nil {
		return store.NetworkOperation{}, invalid("protocol checker is not configured")
	}
	if n.Status == store.NodeDisabled {
		return store.NetworkOperation{}, invalid("node is disabled")
	}
	ip, err := parseIPv4(in.IP)
	if err != nil {
		return store.NetworkOperation{}, err
	}
	in.IP = ip
	if ip == n.IP {
		return store.NetworkOperation{}, invalid("address is already active")
	}
	if in.Mode != "existing_ip" && in.Mode != "ssh" {
		return store.NetworkOperation{}, invalid("mode must be existing_ip or ssh")
	}
	if in.Mode == "ssh" && (n.Runtime == store.RuntimeLocal || in.SSH == nil) {
		return store.NetworkOperation{}, invalid("SSH target is required for a remote node")
	}
	if err = s.unlocked(ctx, id); err != nil {
		return store.NetworkOperation{}, err
	}
	records, err := s.DNS.Lookup(ctx, n.Host)
	if err != nil {
		return store.NetworkOperation{}, err
	}
	if len(records) != 1 || records[0].ID != n.DNSRecordID || records[0].Content != n.IP || records[0].Type != "A" || records[0].Proxied {
		return store.NetworkOperation{}, invalid("DNS differs from active node state; reconcile the previous operation first")
	}

	if err = s.ensureInstallation(ctx, n); err != nil {
		return store.NetworkOperation{}, err
	}
	if in.Mode == "existing_ip" {
		addresses, err := s.Store.ListAddresses(ctx, id)
		if err != nil {
			return store.NetworkOperation{}, err
		}
		found := false
		for _, a := range addresses {
			if a.IP == ip && a.InstallationID == n.ActiveInstallation {
				found = true
			}
		}
		if !found {
			return store.NetworkOperation{}, invalid("address is not in this installation's pool")
		}
	}
	op := store.NetworkOperation{ID: operationID(), NodeID: id, Kind: in.Mode, Stage: "preparing", OldIP: n.IP, NewIP: ip, OldInstallation: n.ActiveInstallation, NewInstallation: n.ActiveInstallation}
	if err = s.Store.CreateNetworkOperation(ctx, &op); err != nil {
		return store.NetworkOperation{}, err
	}
	// Store only public operation metadata; credentials remain in this bounded goroutine.
	bg := s.Background
	if bg == nil {
		bg = context.Background()
	}
	jctx, cancel := context.WithTimeout(bg, 25*time.Minute)
	go func(o store.NetworkOperation) { defer cancel(); s.runRotation(jctx, &o, in) }(op)
	return op, nil
}
func (s *Service) saveStage(ctx context.Context, o *store.NetworkOperation, stage string) error {
	o.Stage = stage
	o.Log += stage + "\n"
	return s.Store.SaveNetworkOperation(ctx, o)
}
func (s *Service) runRotation(ctx context.Context, o *store.NetworkOperation, in RotateIPInput) {
	err := s.performRotation(ctx, o, in)
	end := s.now()
	o.FinishedAt = &end
	if err != nil {
		o.Status = "failed"
		o.Error = healthcheck.SafeError(err)
	} else {
		o.Status = "ok"
		o.Error = ""
	}
	// Persist final state even when the worker context was cancelled.
	persist, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if saveErr := s.Store.SaveNetworkOperation(persist, o); saveErr != nil {
		s.log().Error("save IP operation", "operation", o.ID, "err", saveErr)
	}
}
func (s *Service) performRotation(ctx context.Context, o *store.NetworkOperation, in RotateIPInput) error {
	n, err := s.Store.GetNode(ctx, o.NodeID)
	if err != nil {
		return err
	}
	if in.Mode == "ssh" {
		i := &store.Installation{NodeID: n.ID, Identity: "install-" + o.ID, IP: o.NewIP, State: "candidate"}
		if err = s.Store.CreateInstallation(ctx, i); err != nil {
			return err
		}
		o.NewInstallation = i.ID
		if err = s.saveStage(ctx, o, "installing"); err != nil {
			return err
		}
		if err = s.installCandidate(ctx, n, *i, in); err != nil {
			return err
		}
		if err = s.saveStage(ctx, o, "waiting_config"); err != nil {
			return err
		}
		for {
			candidate, err := s.Store.InstallationByIdentity(ctx, i.Identity)
			if err != nil {
				return err
			}
			current, err := s.Store.GetNode(ctx, n.ID)
			if err != nil {
				return err
			}
			if candidate.ApplyError != "" {
				return errors.New("candidate configuration failed")
			}
			if candidate.LastSeen != nil && candidate.AppliedHash != "" && candidate.AppliedHash == current.DesiredHash {
				break
			}
			timer := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	if err = s.saveStage(ctx, o, "checking"); err != nil {
		return err
	}
	if err = s.checkAddress(ctx, *n, o.NewIP); err != nil {
		return err
	}
	if err = s.saveStage(ctx, o, "switching_dns"); err != nil {
		return err
	}
	record, err := s.DNS.Ensure(ctx, n.Host, o.NewIP, n.DNSRecordID)
	if err != nil {
		return err
	}
	if err = s.Store.SetManagedDNS(ctx, n.ID, n.Host, record.ID); err != nil {
		return err
	}
	if err = s.verifyDNS(ctx, n.Host, o.NewIP); err != nil {
		return err
	}
	if err = s.activateInstallation(ctx, n.ID, o.NewInstallation, o.NewIP); err != nil {
		return err
	}
	addresses, err := s.Store.ListAddresses(ctx, n.ID)
	if err != nil {
		return err
	}
	found := false
	for _, a := range addresses {
		if a.IP == o.NewIP {
			found = true
		}
	}
	if !found {
		if err = s.Store.CreateAddress(ctx, &store.Address{NodeID: n.ID, InstallationID: o.NewInstallation, IP: o.NewIP}); err != nil {
			return err
		}
	}
	if err = s.saveStage(ctx, o, "switched"); err != nil {
		return err
	}
	if err = s.checkAddress(ctx, *n, o.NewIP); err != nil {
		return err
	}
	if in.Mode == "ssh" {
		if err = s.saveStage(ctx, o, "draining"); err != nil {
			return err
		}
		delay := s.DrainDelay
		if delay <= 0 {
			delay = 120 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		// Revoke the old generation even if SSH is unavailable. Never delete the server.
		if err = s.Store.SetInstallationState(ctx, o.OldInstallation, "retired"); err != nil {
			return err
		}
		s.kick()
		source := SSHInput{Host: o.OldIP}
		if in.SourceSSH != nil {
			source = *in.SourceSSH
			if source.Host == "" {
				source.Host = o.OldIP
			}
		}
		key, err := s.DeployKey()
		if err != nil {
			return err
		}
		if err = provision.StopServices(ctx, sshTarget(source), key); err != nil {
			return errors.New("IP switched; old installation revoked, SSH stop failed; retry cleanup with source SSH credentials")
		}
	}
	return s.saveStage(ctx, o, "complete")
}
func (s *Service) checkAddress(ctx context.Context, n store.Node, ip string) error {
	u, err := s.EnsureMonitor(ctx, n.ID)
	if err != nil {
		return err
	}
	ins, err := s.Store.ListInbounds(ctx, n.ID)
	if err != nil {
		return err
	}
	if len(ins) == 0 {
		return invalid("node has no protocols")
	}
	points := map[string]healthcheck.Checker{"panel": s.Checker}
	for point, c := range s.ProbePoints {
		points[point] = c
	}
	for _, in := range ins {
		for point, c := range points {
			if err := s.checkProtocol(ctx, c, healthcheck.Request{IP: ip, Host: n.Host, Inbound: in.Spec, User: *u}); err != nil {
				return fmt.Errorf("%s %s readiness: %w", point, in.Spec.EffectiveTag(), err)
			}
		}
	}
	return nil
}
func (s *Service) verifyDNS(ctx context.Context, host, ip string) error {
	records, err := s.DNS.Lookup(ctx, host)
	if err != nil {
		return err
	}
	if len(records) != 1 || records[0].Content != ip || records[0].Type != "A" || records[0].Proxied || records[0].TTL != 60 {
		return errors.New("DNS update was not confirmed")
	}
	if s.ResolveA != nil {
		return s.ResolveA(ctx, host, ip)
	}
	return nil
}

// RecoverNetworkOperations reconciles interrupted switches; it never replays SSH credentials.
func (s *Service) RecoverNetworkOperations(ctx context.Context) error {
	ops, err := s.Store.ListNetworkOperations(ctx, 0)
	if err != nil {
		return err
	}
	for _, o := range ops {
		if o.Status != "running" {
			continue
		}
		n, err := s.Store.GetNode(ctx, o.NodeID)
		if err != nil {
			return err
		}
		records, err := s.DNS.Lookup(ctx, n.Host)
		if err != nil {
			return err
		}
		if len(records) != 1 {
			return errors.New("cannot reconcile DNS; operation remains locked")
		}
		switch records[0].Content {
		case o.NewIP:
			if err = s.activateInstallation(ctx, n.ID, o.NewInstallation, o.NewIP); err != nil {
				return err
			}
			o.Stage = "switched"
			o.Error = "panel restarted after DNS switch; inspect and retry cleanup"
		case o.OldIP:
			o.Error = "interrupted before DNS switch; retry with SSH credentials if needed"
		default:
			return errors.New("unexpected DNS address; operation remains locked")
		}
		o.Status = "failed"
		end := s.now()
		o.FinishedAt = &end
		if err = s.Store.SaveNetworkOperation(ctx, &o); err != nil {
			return err
		}
	}
	return nil
}

func sshTarget(in SSHInput) provision.SSHTarget {
	key := strings.TrimSpace(in.PrivateKey)
	if key != "" {
		key += "\n"
	}
	return provision.SSHTarget{Host: in.Host, Port: in.Port, User: in.User, Password: in.Password, PrivateKey: []byte(key), HostKeySHA256: in.HostKeySHA256}
}
func (s *Service) installCandidate(ctx context.Context, n *store.Node, i store.Installation, in RotateIPInput) error {
	if _, err := s.EnsureMonitor(ctx, n.ID); err != nil {
		return err
	}
	key, err := s.DeployKey()
	if err != nil {
		return err
	}
	ins, err := s.Store.ListInbounds(ctx, n.ID)
	if err != nil {
		return err
	}
	var state []byte
	for _, ib := range ins {
		if ib.Spec.Hysteria2 != nil && ib.Spec.Hysteria2.TLSMode() == singbox.TLSACME {
			if in.SourceSSH == nil {
				return invalid("source_ssh required to transfer ACME state")
			}
			source := *in.SourceSSH
			if source.Host == "" {
				source.Host = n.IP
			}
			state, err = provision.ExportACME(ctx, sshTarget(source), key)
			if err != nil {
				return errors.New("cannot export ACME state from source")
			}
		}
	}
	target := *in.SSH
	if target.Host == "" {
		target.Host = in.IP
	}
	files, name, cmd, err := s.agentInstallIdentity(ctx, n.ID, true, target.Port, i.Identity)
	if err != nil {
		return err
	}
	if len(state) > 0 {
		files = append(files, provision.File{Path: "acme.tar.gz", Data: state, Mode: 0600})
		cmd = "mkdir -p /var/lib/giocondo-vpn/sing-box/acme && tar xzf acme.tar.gz -C /var/lib/giocondo-vpn/sing-box/acme && " + cmd
	}
	bundle, err := provision.TarGz(name, files, s.now())
	if err != nil {
		return err
	}
	// Installation output may contain credentials; only stage metadata is journaled.
	_, err = provision.Install(ctx, sshTarget(target), key, provision.InstallOptions{Bundle: bundle, Dir: name, Cmd: cmd, KeepKey: target.KeepKey}, discardLog{})
	return err
}

type discardLog struct{}

func (discardLog) Write(b []byte) (int, error) { return len(b), nil }

// ResolveAuthoritative verifies the A response against the domain's authoritative DNS.
func ResolveAuthoritative(ctx context.Context, host, ip string) error {
	labels := strings.Split(host, ".")
	for i := 0; i < len(labels)-1; i++ {
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
			for _, a := range addresses {
				if a == ip {
					return nil
				}
			}
		}
		return errors.New("authoritative DNS does not return the new IP")
	}
	return errors.New("authoritative nameservers not found")
}

func (s *Service) activateInstallation(ctx context.Context, nodeID, id int64, ip string) error {
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	return s.Store.ActivateInstallation(ctx, nodeID, id, ip)
}
