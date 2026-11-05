package panel

import "time"

// UserStatus is a user's lifecycle status.
type UserStatus string

const (
	// UserActive is the active user status.
	UserActive UserStatus = "active"
	// UserDisabled is the administratively disabled user status.
	UserDisabled UserStatus = "disabled"
	// UserExpired is the expired user status.
	UserExpired UserStatus = "expired"
	// UserLimited is the traffic-limited user status.
	UserLimited UserStatus = "limit"
)

// Status defines precedence independently of how a caller presents or enforces it.
func (u User) Status(usage Usage, now time.Time) UserStatus {
	switch {
	case !u.Enabled:
		return UserDisabled
	case u.ExpiresAt != nil && !u.ExpiresAt.After(now):
		return UserExpired
	case u.TrafficLimitBytes > 0 && usage.Total() >= u.TrafficLimitBytes:
		return UserLimited
	default:
		return UserActive
	}
}
