package users

import (
	"context"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// CreateUser creates an enabled user; WG keys are issued immediately if WG nodes exist.
func (s *Service) CreateUser(ctx context.Context, in panel.CreateUserInput) (panel.UserView, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := validName(in.Name); err != nil {
		return panel.UserView{}, err
	}

	if in.TrafficLimitBytes < 0 || in.Days < 0 {
		return panel.UserView{}, operation.Invalid("traffic_limit_bytes and days must not be negative")
	}

	if in.Days > 0 && in.ExpiresAt != nil {
		return panel.UserView{}, operation.Invalid("use either days or expires_at")
	}

	u := panel.User{Name: in.Name, TrafficLimitBytes: in.TrafficLimitBytes, ExpiresAt: in.ExpiresAt, Enabled: true}
	if in.Days > 0 {
		exp := s.hooks.Now().AddDate(0, 0, in.Days).Truncate(time.Second)
		u.ExpiresAt = &exp
	}

	if err := s.users.CreateUser(ctx, &u); err != nil {
		return panel.UserView{}, err
	}

	if err := s.wg.EnsureWG(ctx); err != nil {
		logger.FromCtx(ctx).Error("issue wireguard keys", "user", u.Name, "err", err)
	}

	logger.FromCtx(ctx).Info("user created", "user", u.Name)
	s.hooks.Kick()

	v, err := s.GetUser(ctx, u.ID)
	if err == nil {
		s.hooks.EmitUser(panel.EventUserCreated, v)
	}

	return v, err
}
