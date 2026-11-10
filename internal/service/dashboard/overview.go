package dashboard

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// topUsers limits the user ranking on the dashboard.
const topUsers = 5

// Overview returns the counts, the traffic of the last panel.DashboardDays days and what needs attention.
func (s *Service) Overview(ctx context.Context) (panel.Dashboard, error) {
	users, err := s.users.ListUsers(ctx)
	if err != nil {
		return panel.Dashboard{}, err
	}

	nodes, err := s.nodes.ListNodes(ctx)
	if err != nil {
		return panel.Dashboard{}, err
	}

	to := s.hooks.Now().UTC().Truncate(24 * time.Hour)
	from := to.AddDate(0, 0, -(panel.DashboardDays - 1))

	daily, err := s.traffic.UsageByDay(ctx, from, to)
	if err != nil {
		return panel.Dashboard{}, err
	}

	byUser, err := s.traffic.UsageByUsers(ctx, from, to)
	if err != nil {
		return panel.Dashboard{}, err
	}

	byNode, err := s.traffic.UsageByNodes(ctx, from, to)
	if err != nil {
		return panel.Dashboard{}, err
	}

	d := panel.Dashboard{Days: fillDays(daily, from)}
	for _, day := range d.Days {
		d.Period.Up += day.Up
		d.Period.Down += day.Down
	}

	d.Today = d.Days[len(d.Days)-1].Usage

	userNames := make(map[int64]string, len(users))
	for _, u := range users {
		userNames[u.ID] = u.Name
		countUser(&d.Users, u)

		if needsAttention(u) {
			d.AttentionUsers = append(d.AttentionUsers, u)
		}
	}

	nodeNames := make(map[int64]string, len(nodes))
	for _, n := range nodes {
		nodeNames[n.ID] = n.Name
		countNode(&d.Nodes, n)

		if n.Status == string(panel.NodeOffline) || n.ApplyError != "" || n.Warning {
			d.AttentionNodes = append(d.AttentionNodes, n)
		}
	}

	d.TopUsers = ranked(byUser, userNames)
	if len(d.TopUsers) > topUsers {
		d.TopUsers = d.TopUsers[:topUsers]
	}

	d.NodeTraffic = ranked(byNode, nodeNames)

	return d, nil
}

// fillDays returns one entry per day of the window starting at from, with zeros for missing days.
func fillDays(daily []panel.DayUsage, from time.Time) []panel.DayUsage {
	out := make([]panel.DayUsage, panel.DashboardDays)
	for i := range out {
		out[i].Day = from.AddDate(0, 0, i)
	}

	for _, d := range daily {
		if i := int(d.Day.Sub(from) / (24 * time.Hour)); i >= 0 && i < len(out) {
			out[i].Usage = d.Usage
		}
	}

	return out
}

func countUser(c *panel.DashboardUsers, u panel.UserView) {
	c.Total++

	switch panel.UserStatus(u.Status) {
	case panel.UserActive:
		c.Active++
	case panel.UserLimited:
		c.Limited++
	case panel.UserExpired:
		c.Expired++
	case panel.UserDisabled:
		c.Disabled++
	}
}

func countNode(c *panel.DashboardNodes, n panel.NodeView) {
	c.Total++

	switch panel.NodeStatus(n.Status) {
	case panel.NodeOnline:
		c.Online++
	case panel.NodeDisabled:
		c.Disabled++
	}
}

// needsAttention reports an enabled user who is near or past the limit or the term; a disabled user was
// switched off on purpose.
func needsAttention(u panel.UserView) bool {
	switch panel.UserStatus(u.Status) {
	case panel.UserLimited, panel.UserExpired:
		return true
	case panel.UserActive:
		return u.TrafficWarning || u.ExpiresSoon
	}

	return false
}

// ranked joins usage with names, skipping owners that no longer exist, largest total first.
func ranked(usage map[int64]panel.Usage, names map[int64]string) []panel.NamedUsage {
	out := make([]panel.NamedUsage, 0, len(usage))
	for id, u := range usage {
		if name, ok := names[id]; ok && u.Total() > 0 {
			out = append(out, panel.NamedUsage{ID: id, Name: name, Usage: u})
		}
	}

	slices.SortFunc(out, func(a, b panel.NamedUsage) int {
		return cmp.Or(cmp.Compare(b.Total(), a.Total()), cmp.Compare(a.Name, b.Name))
	})

	return out
}
