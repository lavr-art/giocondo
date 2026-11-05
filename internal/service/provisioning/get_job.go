package provisioning

import (
	"context"
	"errors"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// Job returns an installation job: a stored network operation or, when no operation has this id,
// an SSH installation job kept in memory. A store failure is returned, not reported as a missing job.
func (s *Service) Job(ctx context.Context, id string) (panel.JobView, error) {
	op, err := s.network.GetNetworkOperation(ctx, id)

	switch {
	case err == nil:
		return panel.JobView{
			ID:         op.ID,
			NodeID:     op.NodeID,
			Kind:       op.Kind,
			Status:     op.Status,
			Error:      op.Error,
			Log:        op.Log,
			StartedAt:  op.StartedAt,
			FinishedAt: op.FinishedAt,
		}, nil
	case !errors.Is(err, panel.ErrNotFound):
		return panel.JobView{}, err
	}

	j, ok := s.jobs().Get(id)
	if !ok {
		return panel.JobView{}, panel.ErrNotFound
	}

	return j, nil
}
