package provisioning

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// DeployKey returns the node deploy key.
func (s *Service) DeployKey(ctx context.Context) (panel.DeployKeyView, error) {
	if s.remote == nil {
		return panel.DeployKeyView{}, operation.Invalid("PKI is not configured (PANEL_PKI_DIR)")
	}

	return s.remote.PublicDeployKey(ctx)
}
