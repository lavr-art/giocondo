package web

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// Network provides node network operations.
type Network interface {
	AddAddress(ctx context.Context, id int64, raw string) (panel.Address, error)
	DeleteAddress(ctx context.Context, id, addressID int64) error
	EnableDNS(ctx context.Context, id int64, confirm bool) (panel.NodeView, error)
	ResumeIP(ctx context.Context, id, action string, in panel.ResumeIPInput) (panel.NetworkOperation, error)
	RotateIP(ctx context.Context, id int64, in panel.RotateIPInput) (panel.NetworkOperation, error)
}

// Provisioning provides provisioning operations.
type Provisioning interface {
	AgentAddress() string
	AgentBundle(ctx context.Context, id int64) ([]byte, string, error)
	AptRepoURL() string
	CloudInit(ctx context.Context, id int64) ([]byte, string, error)
	DeployKey(ctx context.Context) (panel.DeployKeyView, error)
	Job(ctx context.Context, id string) (panel.JobView, error)
	ProvisionSSH(ctx context.Context, id int64, in panel.SSHInput) (panel.JobView, error)
}

// Hosts provides host operations.
type Hosts interface {
	CreateHost(ctx context.Context, nodeID int64, in panel.HostInput) (panel.HostView, error)
	DeleteHost(ctx context.Context, id int64) (int64, error)
}

// Nodes provides node operations.
type Nodes interface {
	CreateNode(ctx context.Context, in panel.CreateNodeInput) (panel.NodeView, error)
	DeleteNode(ctx context.Context, id int64) error
	UpdateNode(ctx context.Context, id int64, in panel.UpdateNodeInput) (panel.NodeView, error)
}

// Tokens provides API token operations.
type Tokens interface {
	CreateToken(ctx context.Context, name, scope string) (panel.TokenView, string, error)
	DeleteToken(ctx context.Context, id int64) error
	ListTokens(ctx context.Context) ([]panel.TokenView, error)
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
	ExportWireGuard(ctx context.Context, userID, nodeID int64) ([]byte, string, error)
	GetNode(ctx context.Context, id int64) (panel.NodeView, error)
	ListNodes(ctx context.Context) ([]panel.NodeView, error)
}

// Dependencies lists the collaborators required by a component.
type Dependencies struct {
	Network      Network
	Provisioning Provisioning
	Hosts        Hosts
	Nodes        Nodes
	Tokens       Tokens
	Users        Users
	Queries      Queries
}
