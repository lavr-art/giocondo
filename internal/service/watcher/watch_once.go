package watcher

import (
	"context"
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// WatchOnce is one pass of the background watcher:
//   - a user became inactive or active again (term, traffic): Kick, so nodes
//     do not wait for a rebuild; a transition to limit/expired emits an event;
//   - warnings about panel.TrafficWarnPercent% of traffic and an approaching term end, once each;
//   - auto-deletion of users expired more than AutoDeleteExpiredDays days ago.
//
// What has been seen and warned about is stored in the database (UserWatch), so
// events are not repeated after a panel restart. node_down / node_up come from the health checks (health.go).
func (s *Service) WatchOnce(ctx context.Context) error {
	users, err := s.users.ListUsers(ctx)
	if err != nil {
		return err
	}

	usage, err := s.users.UsageAll(ctx)
	if err != nil {
		return err
	}

	now := s.hooks.Now()
	activeChanged := false

	var errs []error

	for _, u := range users {
		changed, err := s.watchUser(ctx, u, usage[u.ID], now)
		activeChanged = activeChanged || changed

		if err != nil {
			errs = append(errs, err)
		}
	}

	if activeChanged {
		logger.FromCtx(ctx).Info("active users changed, updating nodes")
		s.hooks.Kick()
	}

	return errors.Join(errs...)
}

func (s *Service) watchUser(
	ctx context.Context,
	u panel.User,
	usage panel.Usage,
	now time.Time,
) (bool, error) {
	v := s.manager.View(u, usage)

	if days := s.autoDeleteExpiredDays; days > 0 && u.ExpiresAt != nil &&
		now.Sub(*u.ExpiresAt) > time.Duration(days)*24*time.Hour {
		logger.FromCtx(ctx).Info("deleting user expired long ago",
			"user", u.Name, "expired_at", u.ExpiresAt, "after_days", days)

		return false, s.manager.DeleteUser(ctx, u.ID)
	}

	w := u.Watch
	activeChanged := false

	if w.LastStatus != v.Status {
		if w.LastStatus != "" {
			activeChanged = (w.LastStatus == string(panel.UserActive)) != (v.Status == string(panel.UserActive))
			s.emitStatusChange(v)
		}

		w.LastStatus = v.Status
	}

	if v.TrafficWarning && !w.WarnedTraffic {
		s.hooks.EmitUser(panel.EventUserTrafficWarning, v)

		w.WarnedTraffic = true
	}

	if v.ExpiresSoon && !w.WarnedExpiry {
		s.hooks.EmitUser(panel.EventUserExpiring, v)

		w.WarnedExpiry = true
	}

	if w == u.Watch {
		return activeChanged, nil
	}

	err := s.users.SetUserWatch(ctx, u.ID, w)
	if errors.Is(err, panel.ErrNotFound) {
		err = nil
	}

	return activeChanged, err
}

func (s *Service) emitStatusChange(v panel.UserView) {
	switch v.Status {
	case string(panel.UserLimited):
		s.hooks.EmitUser(panel.EventUserLimited, v)
	case string(panel.UserExpired):
		s.hooks.EmitUser(panel.EventUserExpired, v)
	}
}
