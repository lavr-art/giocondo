// Package paneltest exposes production application services to integration fixtures.
package paneltest

import (
	"github.com/artemlavr/giocondo/internal/app/panelapp"
	"github.com/artemlavr/giocondo/internal/service/healthcheck"
	"github.com/artemlavr/giocondo/internal/service/hosts"
	"github.com/artemlavr/giocondo/internal/service/network"
	"github.com/artemlavr/giocondo/internal/service/nodeconfig"
	"github.com/artemlavr/giocondo/internal/service/nodes"
	"github.com/artemlavr/giocondo/internal/service/provisioning"
	"github.com/artemlavr/giocondo/internal/service/tokens"
	"github.com/artemlavr/giocondo/internal/service/users"
	"github.com/artemlavr/giocondo/internal/service/watcher"
)

// Config holds configuration values.
type Config = panelapp.Config

// Dependencies lists the collaborators required by a component.
type Dependencies = panelapp.Dependencies

// Repositories are the panel storage ports grouped by domain.
type Repositories = panelapp.Repositories

// Components groups the services a command runs.
type Components struct {
	App          *panelapp.App
	Users        *users.Service
	Tokens       *tokens.Service
	Queries      *nodes.Reader
	Nodes        *nodes.Service
	Hosts        *hosts.Service
	Credentials  *nodeconfig.Coordinator
	Provisioning *provisioning.Service
	Network      *network.Service
	Health       *healthcheck.Service
	Watcher      *watcher.Service
}

// New builds the application services around d; d.DB is required and stays owned by the caller.
func New(cfg Config, d Dependencies) *Components {
	a := panelapp.New(cfg, d)

	return &Components{App: a, Users: a.UserService(), Tokens: a.TokenService(), Queries: a.NodeQueries(), Nodes: a.NodeService(), Hosts: a.HostService(), Credentials: a.CredentialCoordinator(), Provisioning: a.ProvisioningService(), Network: a.NetworkService(), Health: a.HealthcheckService(), Watcher: a.WatcherService()}
}

// Close releases resources.
func (c *Components) Close() error { return c.App.Close() }
