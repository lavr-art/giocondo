package nodestate

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// RecordStats records reported traffic. The store skips a repeated batch (same instance and seq
// not above the recorded one); that is not an error, so the batch is still acknowledged.
func (s *Service) RecordStats(ctx context.Context, a *Agent, instance string, seq uint64, traffic []UserTraffic) error {
	users, err := s.users.ListUsers(ctx)
	if err != nil {
		return err
	}

	ids := make(map[string]int64, len(users))
	for _, u := range users {
		ids[u.Name] = u.ID
	}

	at := s.now()

	var deltas []panel.TrafficDelta

	for _, ut := range traffic {
		id, ok := ids[ut.Name]
		if !ok {
			logger.FromCtx(ctx).Warn("traffic for unknown user", "node", a.Identity, "user", ut.Name)

			continue
		}

		if ut.Up < 0 || ut.Down < 0 {
			continue
		}

		deltas = append(deltas, panel.TrafficDelta{UserID: id, NodeID: a.Node.ID, At: at, Up: ut.Up, Down: ut.Down})
	}

	return s.traffic.RecordTrafficBatch(ctx, a.Node.ID, instance, seq, deltas)
}
