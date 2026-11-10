package panel

import "time"

// DashboardDays is the length of the traffic window on the dashboard, including today.
const DashboardDays = 30

// Dashboard is the overview on the admin home page. Traffic counts the raw daily counters of the last
// DashboardDays days, so it ignores traffic resets that only move a user's limit offset.
type Dashboard struct {
	Users  DashboardUsers
	Nodes  DashboardNodes
	Today  Usage
	Period Usage
	// Days has one entry per day of the window, oldest first, zero for days without traffic.
	Days []DayUsage
	// TopUsers and NodeTraffic are ordered by total traffic in the window, largest first.
	TopUsers    []NamedUsage
	NodeTraffic []NamedUsage
	// AttentionUsers are near or past their limit or term; AttentionNodes are offline or failing.
	AttentionUsers []UserView
	AttentionNodes []NodeView
}

// DashboardUsers counts users by status.
type DashboardUsers struct {
	Total, Active, Limited, Expired, Disabled int
}

// DashboardNodes counts nodes by status.
type DashboardNodes struct {
	Total, Online, Disabled int
}

// DayUsage is the traffic of one UTC day.
type DayUsage struct {
	Usage

	Day time.Time
}

// NamedUsage is the traffic of a user or a node.
type NamedUsage struct {
	Usage

	ID   int64
	Name string
}
