package users

import (
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// View builds the API view.
func (s *Service) View(u panel.User, usage panel.Usage) panel.UserView {
	v := panel.UserView{
		ID: u.ID, Name: u.Name, Enabled: u.Enabled, TrafficLimitBytes: u.TrafficLimitBytes,
		UsedUpBytes: usage.Up, UsedDownBytes: usage.Down, UsedBytes: usage.Total(),
		ExpiresAt: u.ExpiresAt, CreatedAt: u.CreatedAt, HasWireGuard: u.WG != nil,
		SubscriptionURL: strings.TrimRight(s.subURL, "/") + "/sub/" + u.Token,
	}

	v.Status = string(u.Status(usage, s.hooks.Now()))
	if v.Status == StatusActive {
		v.TrafficWarning = u.TrafficLimitBytes > 0 && usage.Total()*100 >= u.TrafficLimitBytes*panel.TrafficWarnPercent
		v.ExpiresSoon = u.ExpiresAt != nil && u.ExpiresAt.Sub(s.hooks.Now()) <= panel.ExpiryWarnBefore
	}

	return v
}
