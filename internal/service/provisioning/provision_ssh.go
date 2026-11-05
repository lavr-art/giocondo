package provisioning

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// ProvisionSSH runs InstallSSH in the background and returns the job at once; progress is in Job(id).
func (s *Service) ProvisionSSH(ctx context.Context, id int64, in panel.SSHInput) (panel.JobView, error) {
	if _, err := s.remoteNode(ctx, id); err != nil {
		return panel.JobView{}, err
	}

	return s.jobs().Start(ctx, id, "ssh", func(ctx context.Context, log *jobLog) error {
		return s.InstallSSH(ctx, id, in, log)
	}), nil
}
