package admin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/artemlavr/giocondo/internal/healthcheck"
	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
)

const DefaultHealthInterval = 5 * time.Minute

// EnsureMonitor reserves credentials outside the public users/subscription tables.
func (s *Service) EnsureMonitor(ctx context.Context, nodeID int64) (*singbox.User, error) {
	store.WGAllocationMu.Lock()
	defer store.WGAllocationMu.Unlock()
	existing, err := s.Store.MonitorCredentials(ctx, nodeID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	uuid, err := singbox.NewUUID()
	if err != nil {
		return nil, err
	}
	u := singbox.User{Name: fmt.Sprintf("giocondo-vpn-probe-%d", nodeID), UUID: uuid}
	ins, err := s.Store.ListInbounds(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	for _, in := range ins {
		if in.Spec.Type != singbox.TypeWireGuard {
			continue
		}
		users, err := s.Store.ListUsers(ctx)
		if err != nil {
			return nil, err
		}
		taken := []string{}
		for _, other := range users {
			if other.WG != nil {
				taken = append(taken, other.WG.Address)
			}
		}
		nodes, err := s.Store.ListNodes(ctx)
		if err != nil {
			return nil, err
		}
		for _, n := range nodes {
			m, e := s.Store.MonitorCredentials(ctx, n.ID)
			if e == nil && m.WG != nil {
				taken = append(taken, m.WG.Address)
			} else if e != nil && !errors.Is(e, store.ErrNotFound) {
				return nil, e
			}
		}
		addr, err := singbox.AllocateWGAddress(in.Spec.WireGuard.WGSubnet(), taken)
		if err != nil {
			return nil, err
		}
		priv, pub, err := singbox.NewWGKeypair()
		if err != nil {
			return nil, err
		}
		psk, err := singbox.NewPresharedKey()
		if err != nil {
			return nil, err
		}
		u.WG = &singbox.WGPeer{Address: addr, PrivateKey: priv, PublicKey: pub, PresharedKey: psk}
	}
	if err = s.Store.SaveMonitorCredentials(ctx, nodeID, u); err != nil {
		return nil, err
	}
	s.kick()
	return &u, nil
}

func (s *Service) CheckNodes(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultHealthInterval
	}
	timer := time.NewTicker(interval)
	defer timer.Stop()
	for {
		if err := s.CheckNodesOnce(ctx); err != nil && ctx.Err() == nil {
			s.log().Error("protocol health check", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
func (s *Service) CheckNodesOnce(ctx context.Context) error {
	if s.Checker == nil {
		return nil
	}
	s.healthMu.Lock()
	defer s.healthMu.Unlock()
	nodes, err := s.Store.ListNodes(ctx)
	if err != nil {
		return err
	}
	type task struct {
		n     store.Node
		ip    string
		in    singbox.Inbound
		u     singbox.User
		point string
		c     healthcheck.Checker
	}
	var tasks []task
	for _, n := range nodes {
		if n.Status == store.NodeDisabled || n.Status == store.NodeNew || n.IP == "" {
			continue
		}
		u, err := s.EnsureMonitor(ctx, n.ID)
		if err != nil {
			return err
		}
		ins, err := s.Store.ListInbounds(ctx, n.ID)
		if err != nil {
			return err
		}
		addresses, err := s.Store.ListAddresses(ctx, n.ID)
		if err != nil {
			return err
		}
		ips := map[string]bool{n.IP: true}
		for _, a := range addresses {
			if a.InstallationID == n.ActiveInstallation || a.InstallationID == 0 {
				ips[a.IP] = true
			}
		}
		points := map[string]healthcheck.Checker{"panel": s.Checker}
		for name, c := range s.ProbePoints {
			points[name] = c
		}
		for ip := range ips {
			for _, in := range ins {
				for point, c := range points {
					tasks = append(tasks, task{n, ip, in.Spec, *u, point, c})
				}
			}
		}
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for _, t := range tasks {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			err := s.checkProtocol(ctx, t.c, healthcheck.Request{IP: t.ip, Host: t.n.Host, Inbound: t.in, User: t.u})
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, healthcheck.ErrUnavailable) {
				s.log().Warn("probe point unavailable", "point", t.point)
				return
			}
			_, e := s.Store.RecordProbe(ctx, store.ProbeResult{NodeID: t.n.ID, IP: t.ip, Tag: t.in.EffectiveTag(), Point: t.point, Error: healthcheck.SafeError(err), CheckedAt: s.now()})
			if e != nil {
				mu.Lock()
				errs = append(errs, e)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	for _, n := range nodes {
		if n.Status == store.NodeDisabled || n.Status == store.NodeNew {
			continue
		}
		if err := s.healthEvent(ctx, n); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
func (s *Service) healthEvent(ctx context.Context, n store.Node) error {
	s.networkMu.Lock()
	defer s.networkMu.Unlock()
	current, err := s.Store.GetNode(ctx, n.ID)
	if err != nil {
		return err
	}
	n = *current
	if n.Status == store.NodeNew || n.Status == store.NodeDisabled {
		return nil
	}
	probes, err := s.Store.ListProbes(ctx, n.ID)
	if err != nil {
		return err
	}
	down, found := false, false
	for _, p := range probes {
		if (p.Point != "panel" && s.ProbePoints[p.Point] == nil) || p.IP != n.IP || p.CheckedAt.Before(s.now().Add(-2*max(s.healthInterval(), DefaultHealthInterval))) {
			continue
		}
		found = true
		if p.Failures >= 3 {
			down = true
		}
	}
	if !found || down == n.DownNotified {
		return nil
	}
	if err = s.Store.SetNodeDownNotified(ctx, n.ID, down); err != nil {
		return err
	}
	v, err := s.GetNode(ctx, n.ID)
	if err != nil {
		return err
	}
	typ := EventNodeUp
	if down {
		typ = EventNodeDown
	}
	s.emit(Event{Type: typ, Node: &v})
	return nil
}
func (s *Service) healthInterval() time.Duration {
	if s.HealthInterval > 0 {
		return s.HealthInterval
	}
	return DefaultHealthInterval
}

// A WireGuard peer has one roaming endpoint: overlapping probes with the same
// monitor key would move it between checking points and produce false failures.
func (s *Service) checkProtocol(ctx context.Context, c healthcheck.Checker, r healthcheck.Request) error {
	if r.Inbound.Type == singbox.TypeWireGuard {
		s.wgProbeMu.Lock()
		defer s.wgProbeMu.Unlock()
	}
	return c.Check(ctx, r)
}
