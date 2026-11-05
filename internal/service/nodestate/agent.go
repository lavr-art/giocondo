package nodestate

import "github.com/artemlavr/giocondo/internal/model/panel"

// Agent is an admitted agent identity. It belongs to one stream and is not safe for concurrent use.
type Agent struct {
	Identity string
	Node     *panel.Node
	// Installation is nil until the identity is registered as an installation; until then state is
	// written to the node itself.
	Installation *panel.Installation
}

// Network is the desired host network state of the installation in use.
type Network struct {
	PrimaryIP string
	Aliases   []string
	UDPPorts  []uint32
}

// UserTraffic is a reported traffic delta of one user, identified by name.
type UserTraffic struct {
	Name     string
	Up, Down int64
}
