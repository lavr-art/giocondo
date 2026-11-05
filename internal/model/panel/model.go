// Package domain contains panel entities and access policy, without persistence or transport dependencies.

package panel

import (
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// Repository errors, shared independently of the persistence adapter.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists") // uniqueness violation
)

// NodeStatus is the node state.
type NodeStatus string

// Node states.
const (
	NodeNew      NodeStatus = "new"
	NodeOnline   NodeStatus = "online"
	NodeOffline  NodeStatus = "offline"
	NodeDisabled NodeStatus = "disabled"
)

// User is a VPN user.
type User struct {
	ID                int64
	Name              string
	Token             string // subscription token, /sub/<token>
	UUID              string
	TrafficLimitBytes int64      // 0 = no limit
	ExpiresAt         *time.Time // nil = no expiry
	Enabled           bool
	CreatedAt         time.Time
	WG                *protocol.WGPeer // nil until WireGuard keys are issued
	UsageOffset       Usage            // traffic before the last ResetUsage; changed only by it
	Watch             UserWatch        // watcher state; changed only by SetUserWatch
}

// UserWatch is what the panel background watcher has already seen and warned about.
type UserWatch struct {
	LastStatus    string // status at the previous check; "" means not checked yet
	WarnedTraffic bool   // the 80% traffic warning was sent
	WarnedExpiry  bool   // the approaching expiry warning was sent
}

// NodeRuntime is where the node agent runs.
type NodeRuntime string

// NodeRuntime values.
const (
	RuntimeRemote NodeRuntime = "remote" // a separate server
	RuntimeLocal  NodeRuntime = "local"  // agent on the panel machine
)

// Node is a VPS with the agent and sing-box.
type Node struct {
	ID        int64
	Name      string
	Host      string // domain or IP clients connect to
	IP        string
	Region    string
	Status    NodeStatus
	Runtime   NodeRuntime // set at creation; empty means RuntimeRemote
	LastSeen  *time.Time
	CreatedAt time.Time
	// Service fields: UpdateNode does not write them, they change through SetNode*.
	DesiredHash        string // sha256 of the config built by the panel
	AppliedHash        string // sha256 of the config on the node
	ApplyError         string // last apply error; "" means applied
	DNSRecordID        string
	ManagedDNS         bool
	ActiveInstallation int64
	DownNotified       bool // node_down was sent
}

// Dirty means the node runs a different config than the panel built.
func (n Node) Dirty() bool { return n.DesiredHash != "" && n.DesiredHash != n.AppliedHash }

// APIToken is a REST API token. The token itself is not stored, only its sha256.
type APIToken struct {
	ID         int64
	Name       string
	Scope      string
	Hash       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// Inbound identifies a node protocol independently of persistence and rendering.
type Inbound struct {
	ID     int64
	NodeID int64
	Spec   protocol.Inbound
}

// TrafficDelta is a user's traffic increment on a node reported by the agent.
type TrafficDelta struct {
	UserID int64
	NodeID int64
	At     time.Time // determines the day (UTC)
	Up     int64
	Down   int64
}

// Usage is total traffic.
type Usage struct {
	Up   int64
	Down int64
}

// Total returns up + down.
func (u Usage) Total() int64 { return u.Up + u.Down }

// Clamp keeps the value non-negative (the offset can exceed the sum when history was deleted with a node).
func (u Usage) Clamp() Usage {
	return Usage{Up: max(u.Up, 0), Down: max(u.Down, 0)}
}

// Address is a node address with its aliases and DNS record.
type Address struct {
	ID             int64  `json:"id"`
	NodeID         int64  `json:"node_id"`
	InstallationID int64  `json:"installation_id"`
	IP             string `json:"ip"`
}

// InstallationState is the lifecycle stage of an installation. A node runs one active installation;
// a rotation brings up a candidate and retires the old one, whose agent is no longer admitted.
type InstallationState string

// Installation states.
const (
	InstallationCandidate InstallationState = "candidate"
	InstallationActive    InstallationState = "active"
	InstallationRetired   InstallationState = "retired"
)

// Installation is a node's installed agent.
type Installation struct {
	ID          int64             `json:"id"`
	NodeID      int64             `json:"node_id"`
	Identity    string            `json:"identity"`
	IP          string            `json:"ip"`
	State       InstallationState `json:"state"`
	LastSeen    *time.Time        `json:"last_seen"`
	AppliedHash string            `json:"applied_hash"`
	ApplyError  string            `json:"apply_error"`
}

// ProbeResult is the outcome of a probe.
type ProbeResult struct {
	NodeID    int64     `json:"node_id"`
	IP        string    `json:"ip"`
	Tag       string    `json:"tag"`
	Point     string    `json:"point"`
	Error     string    `json:"error"`
	Failures  int       `json:"failures"`
	CheckedAt time.Time `json:"checked_at"`
}

// NetworkOperation states stored in NetworkOperation.Status.
const (
	OperationRunning = "running"
	OperationOK      = "ok"
	OperationFailed  = "failed"
)

// Rotation modes accepted in RotateIPInput.Mode.
const (
	RotateExistingIP = "existing_ip"
	RotateSSH        = "ssh"
)

// NetworkOperation deliberately contains no SSH credentials.
type NetworkOperation struct {
	ID              string     `json:"id"`
	NodeID          int64      `json:"node_id"`
	Kind            string     `json:"kind"`
	Status          string     `json:"status"`
	Stage           string     `json:"stage"`
	OldIP           string     `json:"old_ip"`
	NewIP           string     `json:"new_ip"`
	OldInstallation int64      `json:"old_installation"`
	NewInstallation int64      `json:"new_installation"`
	Error           string     `json:"error,omitempty"`
	Log             string     `json:"log"`
	StartedAt       time.Time  `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
}

// Host is an inbound address for the subscription instead of the node host.
type Host struct {
	ID        int64
	InboundID int64
	Address   string // domain or IP
	Port      uint16 // 0 means the inbound port
	SNI       string // empty means the protocol default
	Path      string // for ws/grpc transports; unused by the current protocols
	Sort      int
}
