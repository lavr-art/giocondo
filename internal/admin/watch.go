package admin

import (
	"context"
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/store"
)

// Watch runs WatchOnce every interval until ctx is cancelled.
func (s *Service) Watch(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.WatchOnce(ctx); err != nil && ctx.Err() == nil {
			s.log().Error("watch", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// WatchOnce is one pass of the background watcher:
//   - a user became inactive or active again (term, traffic): Kick, so nodes
//     do not wait for a rebuild; a transition to limit/expired emits an event;
//   - warnings about TrafficWarnPercent% of traffic and an approaching term end, once each;
//   - auto-deletion of users expired more than AutoDeleteExpiredDays days ago;
//   - node_down / node_up.
//
// What has been seen and warned about is stored in the database (UserWatch, DownNotified), so
// events are not repeated after a panel restart.
func (s *Service) WatchOnce(ctx context.Context) error {
	users, err := s.Store.ListUsers(ctx)
	if err != nil {
		return err
	}
	usage, err := s.Store.UsageAll(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	activeChanged := false
	var errs []error
	for _, u := range users {
		v := s.view(u, usage[u.ID])
		if days := s.AutoDeleteExpiredDays; days > 0 && u.ExpiresAt != nil &&
			now.Sub(*u.ExpiresAt) > time.Duration(days)*24*time.Hour {
			s.log().Info("deleting user expired long ago", "user", u.Name, "expired_at", u.ExpiresAt, "after_days", days)
			if err := s.DeleteUser(ctx, u.ID); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		w := u.Watch
		if w.LastStatus != v.Status {
			if w.LastStatus != "" { // "" is the first check after the migration: remember silently
				if (w.LastStatus == StatusActive) != (v.Status == StatusActive) {
					activeChanged = true
				}
				switch v.Status {
				case StatusLimit:
					s.emitUser(EventUserLimited, v)
				case StatusExpired:
					s.emitUser(EventUserExpired, v)
				}
			}
			w.LastStatus = v.Status
		}
		if v.TrafficWarning && !w.WarnedTraffic {
			s.emitUser(EventUserTrafficWarning, v)
			w.WarnedTraffic = true
		}
		if v.ExpiresSoon && !w.WarnedExpiry {
			s.emitUser(EventUserExpiring, v)
			w.WarnedExpiry = true
		}
		if w != u.Watch {
			if err := s.Store.SetUserWatch(ctx, u.ID, w); err != nil && !errors.Is(err, store.ErrNotFound) {
				errs = append(errs, err)
			}
		}
	}
	if activeChanged {
		s.log().Info("active users changed, updating nodes")
		s.kick()
	}

	return errors.Join(errs...)
}

func (s *Service) watchNodes(ctx context.Context, now time.Time) error {
	nodes, err := s.Store.ListNodes(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range nodes {
		var typ string
		switch {
		case n.Status == store.NodeOffline && !n.DownNotified && (n.LastSeen == nil || now.Sub(*n.LastSeen) >= NodeDownAfter):
			typ = EventNodeDown
		case n.Status == store.NodeOnline && n.DownNotified:
			typ = EventNodeUp
		default:
			continue
		}
		if err := s.Store.SetNodeDownNotified(ctx, n.ID, typ == EventNodeDown); err != nil {
			errs = append(errs, err)
			continue
		}
		s.log().Info("node "+typ[len("node_"):], "node", n.Name)
		if s.Notify != nil {
			v, err := s.nodeView(ctx, n)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			s.emit(Event{Type: typ, Node: &v})
		}
	}
	return errors.Join(errs...)
}
