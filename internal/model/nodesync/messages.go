// Package nodesync defines transport-independent messages exchanged with a panel.
package nodesync

import "errors"

// SingBoxEngineID identifies the sing-box engine. ApplyConfig always targets it, and the legacy
// single-engine Hello and Health fields describe it.
const SingBoxEngineID = "sing-box"

// EngineRuntimeCapability in Hello tells the panel that Hello and Health carry per-engine states.
const EngineRuntimeCapability = "engine-runtime-v1"

// EnginePhaseBlocked is the EngineState phase of an engine that refuses new configs until it is
// recovered, so the panel does not send it any.
const EnginePhaseBlocked = "blocked"

// ErrNetworkConfigSuperseded ends a queued network command that a newer one replaced before it was
// applied. Its text is the NetworkResult error the panel recognizes as not being a failure.
var ErrNetworkConfigSuperseded = errors.New("network configuration superseded")

// ErrConfigSuperseded ends a queued config that a newer one replaced before it was applied. Its text is
// the ApplyResult error the panel recognizes: such a result is not a rejection of the config, so the
// panel neither records it as an apply error nor stops sending that config.
var ErrConfigSuperseded = errors.New("configuration superseded")

// Hello is the first message an agent sends after connecting.
type Hello struct {
	AgentVersion   string
	SingboxVersion string
	ConfigHash     string
	InstanceID     string
	Capabilities   []string
	Engines        []*EngineState
}

// Health is an agent health report.
type Health struct {
	UptimeSeconds     int64
	Load1             float64
	MemTotalBytes     int64
	MemAvailableBytes int64
	AgentRssBytes     int64
	SingboxActive     bool
	ConfigHash        string
	Engines           []*EngineState
}

// Stats is an agent traffic report.
type Stats struct {
	Seq   uint64
	Users []*UserTraffic
}

// UserTraffic is per-user traffic counters.
type UserTraffic struct {
	Name string
	Up   int64
	Down int64
}

// ApplyConfig is a configuration to apply on a node.
type ApplyConfig struct {
	ConfigJSON []byte
	Hash       string
}

// ApplyResult reports the outcome of an apply request.
type ApplyResult struct {
	Hash  string
	Ok    bool
	Error string
}

// NetworkConfig is the network configuration sent to a node.
type NetworkConfig struct {
	PrimaryIP string
	Aliases   []string
	UDPPorts  []uint32
	Hash      string // Hash of the original protobuf serialization, including unknown fields.
}

// StatsAck acknowledges a stats message.
type StatsAck struct {
	Seq uint64
}

// EngineState is the reported state of an engine.
type EngineState struct {
	ID           string
	Version      string
	Active       bool
	Phase        string
	AppliedHash  string
	RejectedHash string
	Error        string
}

// AgentMessage is a message sent from an agent to the panel.
type AgentMessage struct {
	Hello         *Hello
	Health        *Health
	Stats         *Stats
	ApplyResult   *ApplyResult
	NetworkResult *ApplyResult
}

// PanelMessage is a message sent from the panel to an agent.
type PanelMessage struct {
	ApplyConfig   *ApplyConfig
	NetworkConfig *NetworkConfig
	StatsAck      *StatsAck
}
