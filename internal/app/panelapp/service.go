package panelapp

import (
	"github.com/artemlavr/giocondo/internal/service/healthcheck"
	"github.com/artemlavr/giocondo/internal/service/hosts"
	"github.com/artemlavr/giocondo/internal/service/network"
	"github.com/artemlavr/giocondo/internal/service/nodeconfig"
	"github.com/artemlavr/giocondo/internal/service/nodeprobe"
	"github.com/artemlavr/giocondo/internal/service/nodes"
	"github.com/artemlavr/giocondo/internal/service/nodestate"
	"github.com/artemlavr/giocondo/internal/service/provisioning"
	"github.com/artemlavr/giocondo/internal/service/singbox"
	"github.com/artemlavr/giocondo/internal/service/tokens"
	"github.com/artemlavr/giocondo/internal/service/users"
	"github.com/artemlavr/giocondo/internal/service/watcher"
	"github.com/artemlavr/giocondo/internal/worker/healthsched"
)

// CredentialCoordinator returns the credential coordinator.
func (a *App) CredentialCoordinator() *nodeconfig.Coordinator {
	a.serviceOnce.credentials.Do(
		func() {
			a.credentials = nodeconfig.NewCoordinator(
				a.hooksForServices, a.repositories().Nodes, a.repositories().Users, &a.allocator,
			)
		},
	)

	return a.credentials
}

// NodeState returns the agent link state service; the hub and reconciliation share it, and it
// prepares configs with the allocator the credential coordinator uses.
func (a *App) NodeState() *nodestate.Service {
	a.serviceOnce.nodeState.Do(func() {
		a.nodeState = nodestate.New(
			a.hooksForServices,
			a.repositories().Nodes,
			a.repositories().Users,
			a.repositories().Traffic,
			a.repositories().Network,
			&a.allocator,
			singbox.Options{},
		)
	})

	return a.nodeState
}

// HostService returns the host service.
func (a *App) HostService() *hosts.Service {
	a.serviceOnce.hosts.Do(func() {
		a.hosts = hosts.New(a.hooksForServices, a.repositories().Hosts, a.repositories().Nodes)
	})

	return a.hosts
}

// NodeQueries returns node queries.
func (a *App) NodeQueries() *nodes.Reader {
	a.serviceOnce.queries.Do(func() {
		a.queries = nodes.NewReader(
			a.hooksForServices,
			a.repositories().Nodes,
			a.repositories().Users,
			a.repositories().Network,
			a.repositories().Probes,
			a.HostService(),
			a.cfg.DNSDomain,
			a.deps.DNS != nil,
		)
	})

	return a.queries
}

// ProvisioningService returns the provisioning service.
func (a *App) ProvisioningService() *provisioning.Service {
	a.serviceOnce.provisioning.Do(func() {
		a.provisioning = provisioning.New(
			a.hooksForServices,
			a.repositories().Nodes,
			a.repositories().Network,
			a.deps.Issuer,
			a.deps.Artifacts,
			a.deps.Remote,
			a.cfg.AgentAddr,
			a.cfg.AptRepo,
			a.TaskExecutor(),
			a.CredentialCoordinator(),
		)
	})

	return a.provisioning
}

// ProbeService returns the probe service.
func (a *App) ProbeService() *nodeprobe.Service {
	a.serviceOnce.probes.Do(func() {
		a.probes = nodeprobe.New(
			a.repositories().Nodes, a.deps.Checker, a.deps.ProbePoints, a.CredentialCoordinator(),
		)
	})

	return a.probes
}

// NetworkService returns the network service.
func (a *App) NetworkService() *network.Service {
	a.serviceOnce.network.Do(func() {
		a.network = network.New(
			a.hooksForServices,
			a.repositories().Nodes,
			a.repositories().Network,
			a.deps.DNS,
			a.deps.Checker != nil,
			a.deps.ResolveA,
			a.TaskExecutor(),
			a.cfg.DrainDelay,
			&a.nodeLock,
			a.ProvisioningService(),
			a.NodeQueries(),
			a.HostService(),
			a.ProbeService(),
		)
	})

	return a.network
}

// NodeService returns the node service.
func (a *App) NodeService() *nodes.Service {
	a.serviceOnce.nodes.Do(func() {
		a.nodes = nodes.New(
			a.hooksForServices,
			a.repositories().Nodes,
			&a.nodeLock,
			a.deps.Checker != nil,
			a.CredentialCoordinator(),
			a.NetworkService(),
			a.NodeQueries(),
		)
	})

	return a.nodes
}

// HealthcheckService returns the health check service.
func (a *App) HealthcheckService() *healthcheck.Service {
	a.serviceOnce.health.Do(func() {
		a.health = healthcheck.New(
			a.hooksForServices,
			a.repositories().Nodes,
			a.repositories().Network,
			a.repositories().Probes,
			a.deps.Checker,
			a.deps.ProbePoints,
			healthsched.RunProbes,
			a.cfg.HealthInterval,
			&a.nodeLock,
			a.CredentialCoordinator(),
			a.ProbeService(),
			a.NodeQueries(),
		)
	})

	return a.health
}

// UserService returns the user service.
func (a *App) UserService() *users.Service {
	a.serviceOnce.users.Do(func() {
		a.users = users.New(a.hooksForServices, a.repositories().Users, a.cfg.SubURL, a.CredentialCoordinator())
	})

	return a.users
}

// TokenService returns the token service.
func (a *App) TokenService() *tokens.Service {
	a.serviceOnce.tokens.Do(func() {
		a.tokens = tokens.New(a.hooksForServices, a.repositories().Tokens)
	})

	return a.tokens
}

// WatcherService returns the watcher service.
func (a *App) WatcherService() *watcher.Service {
	a.serviceOnce.watcher.Do(func() {
		a.watcher = watcher.New(
			a.hooksForServices, a.repositories().Users, a.cfg.AutoDeleteExpiredDays, a.UserService(),
		)
	})

	return a.watcher
}
