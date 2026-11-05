package provisioning

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// Runtime installs the agent runtime on a node.
type Runtime interface {
	Install(context.Context, panel.Node, panel.AgentCert) error
}
