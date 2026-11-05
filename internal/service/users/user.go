package users

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/internal/service/singbox"
)

// User statuses shown in the interface.
const (
	StatusActive   = string(panel.UserActive)
	StatusDisabled = string(panel.UserDisabled)
	StatusExpired  = string(panel.UserExpired)
	StatusLimit    = string(panel.UserLimited)
)

func validName(name string) error {
	if protocol.IsMonitorUser(name) {
		return operation.Invalid("reserved user name")
	}

	if !singbox.ValidUserName(name) {
		return operation.Invalid("name %q: use latin letters, digits, '.', '_' or '-' (up to 64)", name)
	}

	return nil
}

// resetWarnings stores the warning flags if they changed.
func (s *Service) resetWarnings(ctx context.Context, u *panel.User, w panel.UserWatch) error {
	if w == u.Watch {
		return nil
	}

	return s.users.SetUserWatch(ctx, u.ID, w)
}

// updated returns the user and emits user_updated.
func (s *Service) updated(ctx context.Context, id int64) (panel.UserView, error) {
	v, err := s.GetUser(ctx, id)
	if err == nil {
		s.hooks.EmitUser(panel.EventUserUpdated, v)
	}

	return v, err
}
