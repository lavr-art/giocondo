package subscription

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/store"
)

// Source is the data a subscription needs. Implemented by store.Store.
type Source interface {
	GetUserByToken(ctx context.Context, token string) (*store.User, error)
	ListNodes(ctx context.Context) ([]store.Node, error)
	ListInbounds(ctx context.Context, nodeID int64) ([]store.Inbound, error)
	UserUsage(ctx context.Context, userID int64) (store.Usage, error)
	ListHosts(ctx context.Context) ([]store.Host, error)
}

// DefaultUpdateIntervalHours is how often a client should refresh the subscription.
const DefaultUpdateIntervalHours = 12

// Handler serves the subscription at GET /sub/{token}.
type Handler struct {
	Source         Source
	Title          string           // profile name in the client
	UpdateInterval int              // hours; 0 means DefaultUpdateIntervalHours
	Now            func() time.Time // for tests; nil means time.Now
	Log            *slog.Logger     // nil — slog.Default()
}

// Register attaches the handler to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /sub/{token}", h)
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

// LoadProfile builds a user's profile by token. Nodes with the disabled status are skipped.
func LoadProfile(ctx context.Context, src Source, token string) (*Profile, error) {
	u, err := src.GetUserByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	usage, err := src.UserUsage(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("usage: %w", err)
	}
	nodes, err := src.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("nodes: %w", err)
	}
	hosts, err := src.ListHosts(ctx)
	if err != nil {
		return nil, fmt.Errorf("hosts: %w", err)
	}
	byInbound := map[int64][]store.Host{}
	for _, h := range hosts {
		byInbound[h.InboundID] = append(byInbound[h.InboundID], h)
	}
	p := &Profile{User: *u, Usage: usage}
	for _, n := range nodes {
		if n.Status == store.NodeDisabled {
			continue
		}
		ins, err := src.ListInbounds(ctx, n.ID)
		if err != nil {
			return nil, fmt.Errorf("inbounds of node %s: %w", n.Name, err)
		}
		np := NodeProfile{Node: n, Hosts: map[string][]store.Host{}}
		for _, in := range ins {
			np.Inbounds = append(np.Inbounds, in.Spec)
			if hs := byInbound[in.ID]; len(hs) > 0 {
				np.Hosts[in.Spec.EffectiveTag()] = hs
			}
		}
		p.Nodes = append(p.Nodes, np)
	}
	return p, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	now := h.now()
	p, err := LoadProfile(r.Context(), h.Source, r.PathValue("token"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, "subscription not found", http.StatusNotFound)
		return
	case err != nil:
		h.log().Error("subscription: load profile", "err", err) // the token is not logged
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Manually disabled users get 403. An expired term or exhausted limit does not close the subscription:
	// the client receives the real subscription-userinfo and shows "out of traffic" or
	// "expired", and still cannot connect because the nodes do not have this user.
	if reason := p.Status(now); reason == "disabled" {
		http.Error(w, "subscription is inactive: "+reason, http.StatusForbidden)
		return
	}

	format, ok := ParseFormat(r.URL.Query().Get("format"))
	if !ok {
		format = DetectFormat(r.UserAgent())
	}
	var (
		body        []byte
		contentType string
		ext         string
	)
	switch format {
	case FormatSingBox:
		body, err = SingBoxJSON(*p)
		contentType, ext = "application/json; charset=utf-8", "json"
	case FormatClash:
		body, err = ClashYAML(*p)
		contentType, ext = "text/yaml; charset=utf-8", "yaml"
	default:
		body, err = LinksBase64(*p)
		contentType, ext = "text/plain; charset=utf-8", "txt"
	}
	if err != nil {
		h.log().Error("subscription: render", "user_id", p.User.ID, "format", format, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	title := h.Title
	if title == "" {
		title = "giocondo-vpn"
	}
	interval := h.UpdateInterval
	if interval <= 0 {
		interval = DefaultUpdateIntervalHours
	}
	hdr := w.Header()
	hdr.Set("Content-Type", contentType)
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("Subscription-Userinfo", p.UserInfo())
	hdr.Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(title)))
	hdr.Set("Profile-Update-Interval", strconv.Itoa(interval))
	hdr.Set("Content-Disposition", contentDisposition(title, ext))
	_, _ = w.Write(body)
}

// contentDisposition gives an ASCII file name and a UTF-8 variant for non-ASCII titles (RFC 6266).
func contentDisposition(title, ext string) string {
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' || r == '/' {
			return '_'
		}
		return r
	}, title)
	return fmt.Sprintf(`attachment; filename="%s.%s"; filename*=UTF-8''%s.%s`, ascii, ext, url.PathEscape(title), ext)
}
