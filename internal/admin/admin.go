// Package admin implements administrator operations on users and nodes. Both the
// REST API (internal/api) and the web interface (internal/web) use it, so the logic lives in one place:
// input validation, Store writes and node notification (Kick) after every change.
package admin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/artemlavr/giocondo/internal/dns"
	"github.com/artemlavr/giocondo/internal/healthcheck"
	"github.com/artemlavr/giocondo/internal/nodehub"
	"github.com/artemlavr/giocondo/internal/provision"
	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
)

// ErrInvalid is an input error (the API answers 400).
var ErrInvalid = errors.New("invalid input")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Service performs administrator operations.
type Service struct {
	Background     context.Context
	DNS            dns.Manager
	DNSDomain      string
	Checker        healthcheck.Checker
	ProbePoints    map[string]healthcheck.Checker
	HealthInterval time.Duration
	ResolveA       func(context.Context, string, string) error
	DrainDelay     time.Duration
	healthMu       sync.Mutex
	wgProbeMu      sync.Mutex
	networkMu      sync.Mutex
	Store          store.Store
	Kick           func()           // rebuild node configs; nil does nothing
	SubURL         string           // https://sub.example.com is the public subscription server address
	AgentAddr      string           // panel gRPC host:port for agents, comma-separated fallbacks (for install instructions)
	AptRepo        string           // apt repository with vpnctl-agent for instructions; empty means DefaultAptRepo
	PKIDir         string           // PKI directory for issuing agent certificates
	Now            func() time.Time // nil — time.Now
	Log            *slog.Logger
	Notify         func(Event) // webhook events; nil means they are not sent
	// AutoDeleteExpiredDays: Watch deletes users whose term expired more than N days ago; 0 disables it.
	AutoDeleteExpiredDays int

	touchMu sync.Mutex
	touched map[int64]time.Time // token -> when last_used_at was last written

	jobsOnce sync.Once
	jobsList *provision.Jobs // background node installations (ProvisionSSH)
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) kick() {
	if s.Kick != nil {
		s.Kick()
	}
}

// User statuses shown in the interface.
const (
	StatusActive   = "active"
	StatusDisabled = "disabled"
	StatusExpired  = "expired"
	StatusLimit    = "limit"
)

// UserView is a user with computed fields for the API and pages.
type UserView struct {
	ID                int64      `json:"id"`
	Name              string     `json:"name"`
	Status            string     `json:"status"`
	Enabled           bool       `json:"enabled"`
	TrafficLimitBytes int64      `json:"traffic_limit_bytes"`
	UsedUpBytes       int64      `json:"used_up_bytes"`
	UsedDownBytes     int64      `json:"used_down_bytes"`
	UsedBytes         int64      `json:"used_bytes"`
	ExpiresAt         *time.Time `json:"expires_at"`
	CreatedAt         time.Time  `json:"created_at"`
	SubscriptionURL   string     `json:"subscription_url"`
	HasWireGuard      bool       `json:"has_wireguard"`
	TrafficWarning    bool       `json:"traffic_warning"` // active and has used TrafficWarnPercent% of the limit
	ExpiresSoon       bool       `json:"expires_soon"`    // active and the term ends sooner than ExpiryWarnBefore
}

func (s *Service) view(u store.User, usage store.Usage) UserView {
	v := UserView{
		ID: u.ID, Name: u.Name, Enabled: u.Enabled, TrafficLimitBytes: u.TrafficLimitBytes,
		UsedUpBytes: usage.Up, UsedDownBytes: usage.Down, UsedBytes: usage.Total(),
		ExpiresAt: u.ExpiresAt, CreatedAt: u.CreatedAt, HasWireGuard: u.WG != nil,
		SubscriptionURL: strings.TrimRight(s.SubURL, "/") + "/sub/" + u.Token,
	}
	switch {
	case !u.Enabled:
		v.Status = StatusDisabled
	case u.ExpiresAt != nil && !u.ExpiresAt.After(s.now()):
		v.Status = StatusExpired
	case u.TrafficLimitBytes > 0 && usage.Total() >= u.TrafficLimitBytes:
		v.Status = StatusLimit
	default:
		v.Status = StatusActive
		v.TrafficWarning = u.TrafficLimitBytes > 0 && usage.Total()*100 >= u.TrafficLimitBytes*TrafficWarnPercent
		v.ExpiresSoon = u.ExpiresAt != nil && u.ExpiresAt.Sub(s.now()) <= ExpiryWarnBefore
	}
	return v
}

// ListUsers returns all users ordered by name.
func (s *Service) ListUsers(ctx context.Context) ([]UserView, error) {
	users, err := s.Store.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	usage, err := s.Store.UsageAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]UserView, 0, len(users))
	for _, u := range users {
		out = append(out, s.view(u, usage[u.ID]))
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// GetUser returns a user.
func (s *Service) GetUser(ctx context.Context, id int64) (UserView, error) {
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	usage, err := s.Store.UserUsage(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	return s.view(*u, usage), nil
}

// UserRecord is the full database user (for the WG config and QR on the user page).
func (s *Service) UserRecord(ctx context.Context, id int64) (*store.User, error) {
	return s.Store.GetUser(ctx, id)
}

// CreateUserInput is a new user. Days > 0 sets the term from now
// (mutually exclusive with ExpiresAt); both empty means no expiry.
type CreateUserInput struct {
	Name              string     `json:"name"`
	TrafficLimitBytes int64      `json:"traffic_limit_bytes"`
	ExpiresAt         *time.Time `json:"expires_at"`
	Days              int        `json:"days"`
}

func validName(name string) error {
	if strings.HasPrefix(name, "vpnctl-probe-") {
		return invalid("reserved user name")
	}
	if !singbox.ValidUserName(name) {
		return invalid("name %q: use latin letters, digits, '.', '_' or '-' (up to 64)", name)
	}
	return nil
}

// CreateUser creates an enabled user; WG keys are issued immediately if WG nodes exist.
func (s *Service) CreateUser(ctx context.Context, in CreateUserInput) (UserView, error) {
	in.Name = strings.TrimSpace(in.Name)
	if err := validName(in.Name); err != nil {
		return UserView{}, err
	}
	if in.TrafficLimitBytes < 0 || in.Days < 0 {
		return UserView{}, invalid("traffic_limit_bytes and days must not be negative")
	}
	if in.Days > 0 && in.ExpiresAt != nil {
		return UserView{}, invalid("use either days or expires_at")
	}
	u := store.User{Name: in.Name, TrafficLimitBytes: in.TrafficLimitBytes, ExpiresAt: in.ExpiresAt, Enabled: true}
	if in.Days > 0 {
		exp := s.now().AddDate(0, 0, in.Days).Truncate(time.Second)
		u.ExpiresAt = &exp
	}
	if err := s.Store.CreateUser(ctx, &u); err != nil {
		return UserView{}, err
	}
	if err := s.ensureWG(ctx); err != nil {
		s.log().Error("issue wireguard keys", "user", u.Name, "err", err)
	}
	s.log().Info("user created", "user", u.Name)
	s.kick()
	v, err := s.GetUser(ctx, u.ID)
	if err == nil {
		s.emitUser(EventUserCreated, v)
	}
	return v, err
}

// ensureWG issues WG keys to everyone who lacks them, in the subnet of the first WG node.
func (s *Service) ensureWG(ctx context.Context) error {
	nodes, err := s.Store.ListNodes(ctx)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		ins, err := s.Store.ListInbounds(ctx, n.ID)
		if err != nil {
			return err
		}
		for _, in := range ins {
			if in.Spec.Type == singbox.TypeWireGuard && in.Spec.WireGuard != nil {
				return nodehub.EnsureWGPeers(ctx, s.Store, in.Spec.WireGuard.WGSubnet())
			}
		}
	}
	return nil
}

// UpdateUserInput is a partial change: nil fields are left as is. ClearExpiry removes the expiry.
type UpdateUserInput struct {
	Name              *string    `json:"name"`
	TrafficLimitBytes *int64     `json:"traffic_limit_bytes"`
	ExpiresAt         *time.Time `json:"expires_at"`
	ClearExpiry       bool       `json:"clear_expiry"`
	Enabled           *bool      `json:"enabled"`
}

// UpdateUser changes the name, limit, term and enabled flag.
func (s *Service) UpdateUser(ctx context.Context, id int64, in UpdateUserInput) (UserView, error) {
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if err := validName(name); err != nil {
			return UserView{}, err
		}
		u.Name = name
	}
	if in.TrafficLimitBytes != nil {
		if *in.TrafficLimitBytes < 0 {
			return UserView{}, invalid("traffic_limit_bytes must not be negative")
		}
		u.TrafficLimitBytes = *in.TrafficLimitBytes
	}
	if in.ClearExpiry && in.ExpiresAt != nil {
		return UserView{}, invalid("use either expires_at or clear_expiry")
	}
	if in.ExpiresAt != nil {
		exp := in.ExpiresAt.UTC()
		u.ExpiresAt = &exp
	}
	if in.ClearExpiry {
		u.ExpiresAt = nil
	}
	if in.Enabled != nil {
		u.Enabled = *in.Enabled
	}
	if err := s.Store.UpdateUser(ctx, u); err != nil {
		return UserView{}, err
	}
	// A new limit or term allows the corresponding warning to be sent again.
	w := u.Watch
	if in.TrafficLimitBytes != nil {
		w.WarnedTraffic = false
	}
	if in.ExpiresAt != nil || in.ClearExpiry {
		w.WarnedExpiry = false
	}
	if err := s.resetWarnings(ctx, u, w); err != nil {
		return UserView{}, err
	}
	s.log().Info("user updated", "user", u.Name)
	s.kick()
	return s.updated(ctx, id)
}

// resetWarnings stores the warning flags if they changed.
func (s *Service) resetWarnings(ctx context.Context, u *store.User, w store.UserWatch) error {
	if w == u.Watch {
		return nil
	}
	return s.Store.SetUserWatch(ctx, u.ID, w)
}

// updated returns the user and emits user_updated.
func (s *Service) updated(ctx context.Context, id int64) (UserView, error) {
	v, err := s.GetUser(ctx, id)
	if err == nil {
		s.emitUser(EventUserUpdated, v)
	}
	return v, err
}

// Extend extends the term by days: from the current term, or from now if it has already expired.
// A user without expiry has nothing to extend.
func (s *Service) Extend(ctx context.Context, id int64, days int) (UserView, error) {
	if days <= 0 || days > 3650 {
		return UserView{}, invalid("days must be between 1 and 3650")
	}
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	if u.ExpiresAt == nil {
		return UserView{}, invalid("user %s has no expiry date", u.Name)
	}
	base := *u.ExpiresAt
	if now := s.now(); base.Before(now) {
		base = now
	}
	exp := base.AddDate(0, 0, days).Truncate(time.Second)
	u.ExpiresAt = &exp
	if err := s.Store.UpdateUser(ctx, u); err != nil {
		return UserView{}, err
	}
	w := u.Watch
	w.WarnedExpiry = false
	if err := s.resetWarnings(ctx, u, w); err != nil {
		return UserView{}, err
	}
	s.log().Info("user extended", "user", u.Name, "days", days, "expires_at", exp)
	s.kick()
	return s.updated(ctx, id)
}

// ResetTraffic resets used traffic (e.g. when a package is renewed).
func (s *Service) ResetTraffic(ctx context.Context, id int64) (UserView, error) {
	u, err := s.Store.GetUser(ctx, id)
	if err != nil {
		return UserView{}, err
	}
	if err := s.Store.ResetUsage(ctx, id); err != nil {
		return UserView{}, err
	}
	w := u.Watch
	w.WarnedTraffic = false
	if err := s.resetWarnings(ctx, u, w); err != nil {
		return UserView{}, err
	}
	s.kick()
	return s.updated(ctx, id)
}

// RotateToken issues a new subscription link; the old one stops working.
func (s *Service) RotateToken(ctx context.Context, id int64) (UserView, error) {
	if _, err := s.Store.RotateToken(ctx, id); err != nil {
		return UserView{}, err
	}
	return s.updated(ctx, id)
}

// DeleteUser deletes a user and their traffic.
func (s *Service) DeleteUser(ctx context.Context, id int64) error {
	v, err := s.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Store.DeleteUser(ctx, id); err != nil {
		return err
	}
	s.log().Info("user deleted", "user", v.Name)
	s.kick()
	s.emitUser(EventUserDeleted, v)
	return nil
}
