package users

import (
	"context"
	"sort"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// ListUsers returns all users ordered by name.
func (s *Service) ListUsers(ctx context.Context) ([]panel.UserView, error) {
	users, err := s.users.ListUsers(ctx)
	if err != nil {
		return nil, err
	}

	usage, err := s.users.UsageAll(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]panel.UserView, 0, len(users))
	for _, u := range users {
		out = append(out, s.View(u, usage[u.ID]))
	}

	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })

	return out, nil
}
