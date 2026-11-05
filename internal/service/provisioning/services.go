package provisioning

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// StartServices starts the node services.
func (s *Service) StartServices(ctx context.Context, in panel.SSHInput) error {
	if s.remote == nil {
		return operation.Invalid("PKI is not configured (PANEL_PKI_DIR)")
	}

	return s.remote.StartServices(ctx, in)
}

// StopServices stops the node services.
func (s *Service) StopServices(ctx context.Context, in panel.SSHInput) error {
	if s.remote == nil {
		return operation.Invalid("PKI is not configured (PANEL_PKI_DIR)")
	}

	return s.remote.StopServices(ctx, in)
}
