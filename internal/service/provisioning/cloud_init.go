package provisioning

import (
	"context"

	"github.com/artemlavr/giocondo/pkg/logger"
)

// CloudInit is user-data for a new server: installs the agent with a new node certificate and ufw.
func (s *Service) CloudInit(ctx context.Context, id int64) ([]byte, string, error) {
	if _, err := s.remoteNode(ctx, id); err != nil {
		return nil, "", err
	}

	a, err := s.agentInstall(ctx, id, true, 22)
	if err != nil {
		return nil, "", err
	}

	data, err := s.artifacts.CloudInit(a)
	if err != nil {
		return nil, "", err
	}

	logger.FromCtx(ctx).Info("cloud-init user-data issued", "node", a.Name)

	return data, a.Name + "-cloud-init.yaml", nil
}
