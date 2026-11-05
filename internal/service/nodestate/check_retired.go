package nodestate

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// CheckRetired returns ErrRetired when the agent's installation was retired after admission.
func (s *Service) CheckRetired(ctx context.Context, a *Agent) error {
	if a.Installation == nil {
		return nil
	}

	i, err := s.network.InstallationByIdentity(ctx, a.Identity)
	if err != nil {
		return err
	}

	if i.State == panel.InstallationRetired {
		return ErrRetired
	}

	return nil
}
