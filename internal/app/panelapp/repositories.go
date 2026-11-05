package panelapp

import (
	"context"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
	hostrepo "github.com/artemlavr/giocondo/internal/repository/sqlite/hosts"
	networkrepo "github.com/artemlavr/giocondo/internal/repository/sqlite/network"
	noderepo "github.com/artemlavr/giocondo/internal/repository/sqlite/nodes"
	proberepo "github.com/artemlavr/giocondo/internal/repository/sqlite/probes"
	tokenrepo "github.com/artemlavr/giocondo/internal/repository/sqlite/tokens"
	trafficrepo "github.com/artemlavr/giocondo/internal/repository/sqlite/traffic"
	userrepo "github.com/artemlavr/giocondo/internal/repository/sqlite/users"
	"github.com/artemlavr/giocondo/internal/service/healthcheck"
	"github.com/artemlavr/giocondo/internal/service/hosts"
	"github.com/artemlavr/giocondo/internal/service/network"
	"github.com/artemlavr/giocondo/internal/service/nodeconfig"
	"github.com/artemlavr/giocondo/internal/service/nodeprobe"
	"github.com/artemlavr/giocondo/internal/service/nodes"
	"github.com/artemlavr/giocondo/internal/service/nodestate"
	"github.com/artemlavr/giocondo/internal/service/provisioning"
	"github.com/artemlavr/giocondo/internal/service/subscription"
	"github.com/artemlavr/giocondo/internal/service/tokens"
	"github.com/artemlavr/giocondo/internal/service/users"
	"github.com/artemlavr/giocondo/internal/service/watcher"
)

// Dependencies are provided once by the composition root. Optional facilities remain disabled when absent.
type Dependencies struct {
	// DB is a database owned by the caller, for service-only use (operator commands and tests).
	DB *sqlite.DB
	// Repositories replace, field by field, the repositories built over the database.
	Repositories Repositories

	Issuer      provisioning.CertificateIssuer
	Artifacts   provisioning.InstallationArtifacts
	Remote      provisioning.RemoteNodes
	DNS         network.DNSManager
	Checker     probe.Checker
	ProbePoints map[string]probe.Checker
	ResolveA    func(context.Context, string, string) error

	Now    func() time.Time
	Notify func(panel.Event)
	Kick   func()
}

// Repositories are the panel storage ports grouped by domain; each joins the ports its consumers declare.
type Repositories struct {
	Users   UserRepository
	Tokens  TokenRepository
	Nodes   NodeRepository
	Network NetworkRepository
	Traffic TrafficRepository
	Probes  ProbeRepository
	Hosts   HostRepository
}

// UserRepository stores users.
type UserRepository interface {
	users.UserRepository
	watcher.WatchRepository
	nodes.UserQueryRepository
	nodeconfig.UserSource
	subscription.UserSource
}

// TokenRepository stores API tokens.
type TokenRepository interface {
	tokens.TokenRepository
}

// NodeRepository stores nodes, their inbounds and monitor credentials.
type NodeRepository interface {
	nodes.NodeOperationRepository
	nodes.NodeQueryRepository
	hosts.NodeRepository
	network.NodeRepository
	provisioning.NodeRepository
	healthcheck.NodeRepository
	nodeprobe.NodeRepository
	nodeconfig.MonitorStore
	nodestate.NodeRepository
	subscription.NodeSource
}

// NetworkRepository stores node addresses, installations and network operations.
type NetworkRepository interface {
	network.NetworkOperationRepository
	nodes.NetworkQueryRepository
	provisioning.NetworkRepository
	healthcheck.AddressRepository
	nodestate.InstallationRepository
}

// TrafficRepository records traffic.
type TrafficRepository interface {
	nodestate.TrafficRepository
}

// ProbeRepository stores node probe results.
type ProbeRepository interface {
	healthcheck.ProbeRepository
	nodes.ProbeQueryRepository
}

// HostRepository stores inbound host overrides.
type HostRepository interface {
	hosts.HostRepository
	subscription.HostSource
}

// supplied reports whether any repository replaces the database one.
func (r Repositories) supplied() bool {
	return r.Users != nil || r.Tokens != nil || r.Nodes != nil || r.Network != nil || r.Traffic != nil ||
		r.Probes != nil || r.Hosts != nil
}

// repositories returns the supplied repositories and builds the missing ones over the database: the
// caller's Dependencies.DB or, once Run opened it, the panel database. Services call it only after the
// database is available, so the result is built once.
func (a *App) repositories() Repositories {
	a.repositoriesOnce.Do(func() {
		r := a.deps.Repositories

		db := a.deps.DB
		if db == nil {
			db = a.db
		}

		if db != nil {
			if r.Users == nil {
				r.Users = userrepo.New(db)
			}

			if r.Tokens == nil {
				r.Tokens = tokenrepo.New(db)
			}

			if r.Nodes == nil {
				r.Nodes = noderepo.New(db)
			}

			if r.Network == nil {
				r.Network = networkrepo.New(db)
			}

			if r.Traffic == nil {
				r.Traffic = trafficrepo.New(db)
			}

			if r.Probes == nil {
				r.Probes = proberepo.New(db)
			}

			if r.Hosts == nil {
				r.Hosts = hostrepo.New(db)
			}
		}

		a.repos = r
	})

	return a.repos
}
