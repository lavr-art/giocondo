// Package provisioning installs and upgrades node agents.
package provisioning

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// monitorIssuer issues the service monitor credentials of a node.
type monitorIssuer interface {
	EnsureMonitor(ctx context.Context, nodeID int64) (*protocol.User, error)
}

// Service installs node agents over SSH and cloud-init and issues their certificates.
type Service struct {
	hooks     operation.Hooks
	nodes     NodeRepository
	network   NetworkRepository
	issuer    CertificateIssuer
	artifacts InstallationArtifacts
	remote    RemoteNodes
	agentAddr string
	aptRepo   string
	executor  operation.TaskExecutor
	monitor   monitorIssuer
	jobsOnce  sync.Once
	jobsList  *jobManager
}

// New returns a provisioning service. agentAddr is the panel address agents connect to; an empty aptRepo
// selects DefaultAptRepo. executor runs background installation jobs.
func New(
	hooks operation.Hooks,
	nodes NodeRepository,
	network NetworkRepository,
	issuer CertificateIssuer,
	artifacts InstallationArtifacts,
	remote RemoteNodes,
	agentAddr string,
	aptRepo string,
	executor operation.TaskExecutor,
	monitor monitorIssuer,
) *Service {
	return &Service{
		hooks:     hooks,
		nodes:     nodes,
		network:   network,
		issuer:    issuer,
		artifacts: artifacts,
		remote:    remote,
		agentAddr: agentAddr,
		aptRepo:   aptRepo,
		executor:  executor,
		monitor:   monitor,
	}
}

// NodeRepository reads and updates the nodes being provisioned.
type NodeRepository interface {
	GetNode(ctx context.Context, id int64) (*panel.Node, error)
	ListInbounds(ctx context.Context, nodeID int64) ([]panel.Inbound, error)
	UpdateNode(ctx context.Context, n *panel.Node) error
}

// NetworkRepository reads node installations and network operations.
type NetworkRepository interface {
	GetNetworkOperation(context.Context, string) (*panel.NetworkOperation, error)
	Installations(context.Context, int64) ([]panel.Installation, error)
}

// CertificateIssuer issues TLS material for a node.
type CertificateIssuer interface {
	Issue(context.Context, string) (panel.AgentCert, error)
}

// InstallationArtifacts builds artifacts for an installation.
type InstallationArtifacts interface {
	Build(panel.InstallSpec) (panel.InstallArtifact, error)
	Archive(panel.InstallArtifact, time.Time) ([]byte, error)
	CloudInit(panel.InstallArtifact) ([]byte, error)
}

// RemoteNodes provides access to remote nodes.
type RemoteNodes interface {
	Install(context.Context, panel.SSHInput, panel.RemoteBundle, io.Writer) (string, error)
	ExportACME(context.Context, panel.SSHInput) ([]byte, error)
	StartServices(context.Context, panel.SSHInput) error
	StopServices(context.Context, panel.SSHInput) error
	PublicDeployKey(context.Context) (panel.DeployKeyView, error)
}
