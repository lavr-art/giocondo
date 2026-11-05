package api

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// Network provides node network operations.
type Network interface {
	AddAddress(ctx context.Context, id int64, raw string) (panel.Address, error)
	DeleteAddress(ctx context.Context, id, addressID int64) error
	EnableDNS(ctx context.Context, id int64, confirm bool) (panel.NodeView, error)
	ListAddresses(ctx context.Context, id int64) ([]panel.Address, error)
	ResumeIP(ctx context.Context, id, action string, in panel.ResumeIPInput) (panel.NetworkOperation, error)
	RotateIP(ctx context.Context, id int64, in panel.RotateIPInput) (panel.NetworkOperation, error)
}

// Tokens provides API token operations.
type Tokens interface {
	Authenticate(ctx context.Context, token string) (panel.TokenView, error)
	CreateToken(ctx context.Context, name, scope string) (panel.TokenView, string, error)
	DeleteToken(ctx context.Context, id int64) error
	ListTokens(ctx context.Context) ([]panel.TokenView, error)
}

// Provisioning provides provisioning operations.
type Provisioning interface {
	CloudInit(ctx context.Context, id int64) ([]byte, string, error)
	DeployKey(ctx context.Context) (panel.DeployKeyView, error)
	IssueAgentCert(ctx context.Context, id int64) (panel.AgentCert, string, error)
	Job(ctx context.Context, id string) (panel.JobView, error)
	ProvisionSSH(ctx context.Context, id int64, in panel.SSHInput) (panel.JobView, error)
}

// Hosts provides host operations.
type Hosts interface {
	CreateHost(ctx context.Context, nodeID int64, in panel.HostInput) (panel.HostView, error)
	DeleteHost(ctx context.Context, id int64) (int64, error)
	ListHosts(ctx context.Context, nodeID int64) ([]panel.HostView, error)
	UpdateHost(ctx context.Context, id int64, in panel.HostInput) (panel.HostView, error)
}

// Nodes provides node operations.
type Nodes interface {
	CreateNode(ctx context.Context, in panel.CreateNodeInput) (panel.NodeView, error)
	DeleteNode(ctx context.Context, id int64) error
	UpdateNode(ctx context.Context, id int64, in panel.UpdateNodeInput) (panel.NodeView, error)
}

// Users provides user operations.
type Users interface {
	CreateUser(ctx context.Context, in panel.CreateUserInput) (panel.UserView, error)
	DeleteUser(ctx context.Context, id int64) error
	Extend(ctx context.Context, id int64, days int) (panel.UserView, error)
	GetUser(ctx context.Context, id int64) (panel.UserView, error)
	ListUsers(ctx context.Context) ([]panel.UserView, error)
	ResetTraffic(ctx context.Context, id int64) (panel.UserView, error)
	RotateToken(ctx context.Context, id int64) (panel.UserView, error)
	UpdateUser(ctx context.Context, id int64, in panel.UpdateUserInput) (panel.UserView, error)
}

// Queries provides read access to stored entities.
type Queries interface {
	GetNode(ctx context.Context, id int64) (panel.NodeView, error)
	ListNodes(ctx context.Context) ([]panel.NodeView, error)
	PreviewDNS(ctx context.Context, id int64) (panel.DNSPreview, error)
}

// Dependencies lists the collaborators required by a component.
type Dependencies struct {
	Network      Network
	Tokens       Tokens
	Provisioning Provisioning
	Hosts        Hosts
	Nodes        Nodes
	Users        Users
	Queries      Queries
}
