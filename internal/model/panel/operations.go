package panel

import (
	"time"

	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// UserView is a user with computed fields for the API and pages.
type UserView struct {
	ID                int64      `json:"id"`
	Name              string     `json:"name"`
	Status            string     `json:"status"`
	Enabled           bool       `json:"enabled"`
	TrafficLimitBytes int64      `json:"traffic_limit_bytes"`
	UsedUpBytes       int64      `json:"used_up_bytes"`
	UsedDownBytes     int64      `json:"used_down_bytes"`
	UsedBytes         int64      `json:"used_bytes"`
	ExpiresAt         *time.Time `json:"expires_at"`
	CreatedAt         time.Time  `json:"created_at"`
	SubscriptionURL   string     `json:"subscription_url"`
	HasWireGuard      bool       `json:"has_wireguard"`
	TrafficWarning    bool       `json:"traffic_warning"` // active and has used TrafficWarnPercent% of the limit
	ExpiresSoon       bool       `json:"expires_soon"`    // active and the term ends sooner than ExpiryWarnBefore
}

// CreateUserInput is a new user. Days > 0 sets the term from now (mutually exclusive with ExpiresAt);
// both empty means no expiry.
type CreateUserInput struct {
	Name              string     `json:"name"`
	TrafficLimitBytes int64      `json:"traffic_limit_bytes"`
	ExpiresAt         *time.Time `json:"expires_at"`
	Days              int        `json:"days"`
}

// UpdateUserInput is a partial user change: nil fields are left as is. ClearExpiry removes the expiry.
type UpdateUserInput struct {
	Name              *string    `json:"name"`
	TrafficLimitBytes *int64     `json:"traffic_limit_bytes"`
	ExpiresAt         *time.Time `json:"expires_at"`
	ClearExpiry       bool       `json:"clear_expiry"`
	Enabled           *bool      `json:"enabled"`
}

// InboundView is a node protocol in a list.
type InboundView struct {
	Tag  string               `json:"tag"`
	Type protocol.InboundType `json:"type"`
	Port uint16               `json:"port"`
}

// NodeView is a node for the API and pages.
type NodeView struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Host    string `json:"host"`
	IP      string `json:"ip"`
	Region  string `json:"region"`
	Status  string `json:"status"`
	Runtime string `json:"runtime"` // remote | local
	// Dirty means the node runs a different config than the panel built (pending or failed to apply).
	Dirty          bool               `json:"dirty"`
	ApplyError     string             `json:"apply_error"` // error of the last apply on the node
	LastSeen       *time.Time         `json:"last_seen"`
	CreatedAt      time.Time          `json:"created_at"`
	Inbounds       []InboundView      `json:"inbounds"`
	Hosts          []HostView         `json:"hosts"`
	ManagedDNS     bool               `json:"managed_dns"`
	Addresses      []Address          `json:"addresses"`
	Probes         []ProbeResult      `json:"probes"`
	Warning        bool               `json:"warning"`
	AgentConnected bool               `json:"agent_connected"`
	DNSPreview     *DNSPreview        `json:"dns_preview,omitempty"`
	Operations     []NetworkOperation `json:"operations"`
}

// CreateNodeInput is a new node. A protocol is enabled if its block is not nil; empty fields get default
// values, keys and certificates are generated.
type CreateNodeInput struct {
	Name      string       `json:"name"`
	Host      string       `json:"host"`
	IP        string       `json:"ip"`
	Region    string       `json:"region"`
	VLESS     *VLESSInput  `json:"vless_reality"`
	Hysteria2 *Hy2Input    `json:"hysteria2"`
	WireGuard *WGNodeInput `json:"wireguard"`
	// Runtime is remote (default) or local; only `panel local-node` creates local.
	Runtime NodeRuntime `json:"-"`
}

// VLESSInput is user-supplied VLESS Reality settings.
type VLESSInput struct {
	Port       uint16 `json:"port"`        // 443
	Dest       string `json:"dest"`        // www.microsoft.com:443
	ServerName string `json:"server_name"` // www.microsoft.com
}

// Hy2Input is user-supplied Hysteria2 settings.
type Hy2Input struct {
	Port      uint16 `json:"port"` // 8443
	ACME      bool   `json:"acme"` // Let's Encrypt certificate instead of a self-signed one (host must be a domain)
	ACMEEmail string `json:"acme_email"`
	Obfs      bool   `json:"obfs"` // salamander with a random password
}

// WGNodeInput is user-supplied WireGuard node settings.
type WGNodeInput struct {
	Port uint16 `json:"port"` // 51820
}

// UpdateNodeInput is a partial node change. Disabled=true removes the node from subscriptions and
// disconnects its agent; false brings it back.
type UpdateNodeInput struct {
	Host     *string `json:"host"`
	IP       *string `json:"ip"`
	Region   *string `json:"region"`
	Disabled *bool   `json:"disabled"`
}

// HostView is a host override: the address clients receive in the subscription instead of the node host.
type HostView struct {
	ID         int64                `json:"id"`
	InboundTag string               `json:"inbound"`
	Type       protocol.InboundType `json:"type"`
	Address    string               `json:"address"`
	Port       uint16               `json:"port"` // 0 means the inbound port
	SNI        string               `json:"sni"`
	Path       string               `json:"path"`
	Sort       int                  `json:"sort"`
}

// HostInput is a new override or its full replacement (the inbound does not change on update).
type HostInput struct {
	Inbound string `json:"inbound"` // node inbound tag, e.g. vless
	Address string `json:"address"`
	Port    uint16 `json:"port"`
	SNI     string `json:"sni"`
	Path    string `json:"path"`
	Sort    int    `json:"sort"`
}

// DNSPreview is a preview of a DNS change.
type DNSPreview struct {
	Host        string `json:"host"`
	IP          string `json:"ip"`
	CurrentHost string `json:"current_host"`
	Managed     bool   `json:"managed"`
}

// RotateIPInput describes a network rotation request.
type RotateIPInput struct {
	Mode      string    `json:"mode"`
	IP        string    `json:"ip"`
	SSH       *SSHInput `json:"ssh,omitempty"`
	SourceSSH *SSHInput `json:"source_ssh,omitempty"`
}

// ResumeIPInput describes a network resume request.
type ResumeIPInput struct {
	SourceSSH *SSHInput `json:"source_ssh,omitempty"`
}

// AgentCert holds the certificates for a node agent.
type AgentCert struct {
	CACert    string `json:"ca_crt"`
	AgentCert string `json:"agent_crt"`
	AgentKey  string `json:"agent_key"`
	PanelAddr string `json:"panel_addr"` // AGENT_PANEL_ADDR
}

// DeployKeyView is the redacted view of a deploy key.
type DeployKeyView struct{ AuthorizedKey string }

// InstallFile is a file shipped with an installation.
type InstallFile struct {
	Path string
	Mode int64
	Data []byte
}

// InstallSpec describes what to install.
type InstallSpec struct {
	Name      string
	Cert      AgentCert
	Repo      string
	Inbounds  []protocol.Inbound
	Firewall  bool
	SSHPort   int
	ACMEState []byte
}

// InstallArtifact is a built installation artifact.
type InstallArtifact struct {
	Name    string
	Files   []InstallFile
	Command string
}

// RemoteBundle is a bundle of artifacts delivered to a node.
type RemoteBundle struct {
	Archive   []byte
	Directory string
	Command   string
	KeepKey   bool
}

// SSHInput is the SSH access for installing the agent on your own server. The password and key are used
// once and are never stored.
type SSHInput struct {
	Host          string `json:"host"` // empty means the node IP or host
	Port          int    `json:"port"` // 0 — 22
	User          string `json:"user"` // empty means root
	Password      string `json:"password"`
	PrivateKey    string `json:"private_key"`
	HostKeySHA256 string `json:"host_key_sha256"` // "SHA256:…"; empty means trust on first connection
	NoFirewall    bool   `json:"no_firewall"`     // do not enable ufw
	KeepKey       bool   `json:"keep_key"`        // keep the panel key in authorized_keys
}

// JobView is a provisioning job snapshot for the API and pages.
type JobView struct {
	ID         string     `json:"id"`
	NodeID     int64      `json:"node_id"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	Log        string     `json:"log"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

// TokenView is an API token without the secret.
type TokenView struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Scope      string     `json:"scope"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

// Event is a server-sent event describing a state change.
type Event struct {
	Type string    `json:"event"`
	Time time.Time `json:"time"`
	User *UserView `json:"user,omitempty"`
	Node *NodeView `json:"node,omitempty"`
}

// DNSRecord is a single DNS record.
type DNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}
