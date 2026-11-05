package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// probeTask is one protocol probe of one node address from one probe point.
type probeTask struct {
	node    panel.Node
	ip      string
	inbound protocol.Inbound
	monitor protocol.User // service credentials, never part of user subscriptions
	point   string        // probe point name stored in the result: "panel" or an external point
	checker probe.Checker
}

// CheckNodesOnce probes every node once. A node whose probes cannot be prepared is skipped and
// reported in the returned error, so it does not stop the checks of the other nodes.
func (s *Service) CheckNodesOnce(ctx context.Context) error {
	if s.checker == nil {
		return nil
	}

	if s.runProbes == nil {
		return errors.New("probe runner is not configured")
	}

	s.healthMu.Lock()
	defer s.healthMu.Unlock()

	nodes, err := s.nodes.ListNodes(ctx)
	if err != nil {
		return err
	}

	tasks, prepareErr := s.probeTasks(ctx, nodes)

	var (
		mu   sync.Mutex
		errs = []error{prepareErr}
	)

	recordErr := func(err error) {
		mu.Lock()
		defer mu.Unlock()

		errs = append(errs, err)
	}

	probes := make([]func(context.Context), 0, len(tasks))
	for _, t := range tasks {
		probes = append(probes, func(work context.Context) { s.runProbe(work, t, recordErr) })
	}

	if err := s.runProbes(ctx, probes); err != nil {
		return err
	}

	for _, n := range nodes {
		if n.Status == panel.NodeDisabled || n.Status == panel.NodeNew {
			continue
		}

		if err := s.HealthEvent(ctx, n); err != nil {
			errs = append(errs, fmt.Errorf("node %s: %w", n.Name, err))
		}
	}

	return errors.Join(errs...)
}

// probeTasks lists the probe tasks of every checked node and joins the errors of the nodes whose
// tasks could not be prepared.
func (s *Service) probeTasks(ctx context.Context, nodes []panel.Node) ([]probeTask, error) {
	var (
		tasks []probeTask
		errs  []error
	)

	for _, n := range nodes {
		if n.Status == panel.NodeDisabled || n.Status == panel.NodeNew || n.IP == "" {
			continue
		}

		nodeTasks, err := s.nodeProbeTasks(ctx, n)
		if err != nil {
			errs = append(errs, fmt.Errorf("node %s: %w", n.Name, err))

			continue
		}

		tasks = append(tasks, nodeTasks...)
	}

	return tasks, errors.Join(errs...)
}

// nodeProbeTasks lists every address of the active installation × protocol × probe point of node.
func (s *Service) nodeProbeTasks(ctx context.Context, node panel.Node) ([]probeTask, error) {
	monitor, err := s.monitor.EnsureMonitor(ctx, node.ID)
	if err != nil {
		return nil, err
	}

	inbounds, err := s.nodes.ListInbounds(ctx, node.ID)
	if err != nil {
		return nil, err
	}

	addresses, err := s.network.ListAddresses(ctx, node.ID)
	if err != nil {
		return nil, err
	}

	ips := map[string]bool{node.IP: true}
	for _, a := range addresses {
		if a.InstallationID == node.ActiveInstallation || a.InstallationID == 0 {
			ips[a.IP] = true
		}
	}

	points := map[string]probe.Checker{"panel": s.checker}
	maps.Copy(points, s.probePoints)

	var tasks []probeTask

	for ip := range ips {
		for _, in := range inbounds {
			for point, checker := range points {
				tasks = append(tasks, probeTask{
					node:    node,
					ip:      ip,
					inbound: in.Spec,
					monitor: *monitor,
					point:   point,
					checker: checker,
				})
			}
		}
	}

	return tasks, nil
}

// runProbe records one probe result; an unavailable probe point and cancellation record nothing.
func (s *Service) runProbe(ctx context.Context, t probeTask, recordErr func(error)) {
	req := probe.Request{IP: t.ip, Host: t.node.Host, Inbound: t.inbound, User: t.monitor}

	err := s.prober.CheckProtocol(ctx, t.checker, req)
	if ctx.Err() != nil {
		return
	}

	if errors.Is(err, probe.ErrUnavailable) {
		logger.FromCtx(ctx).Warn("probe point unavailable", "point", t.point)

		return
	}

	result := panel.ProbeResult{
		NodeID: t.node.ID, IP: t.ip, Tag: t.inbound.EffectiveTag(), Point: t.point,
		Error: operation.SafeError(err), CheckedAt: s.hooks.Now(),
	}
	if _, err := s.probes.RecordProbe(ctx, result); err != nil {
		recordErr(err)
	}
}
