// Package subapi serves subscription profiles at GET /sub/{token}.
package subapi

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/subscription"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// Handler serves the subscription at GET /sub/{token}.
type Handler struct {
	Source subscription.Source
	Now    func() time.Time // for tests; nil means time.Now
}

// Register attaches the handler to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /sub/{token}", h)
}

// ServeHTTP serves the subscription of the user identified by the token. Disabled nodes are skipped.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	now := h.now()

	p, err := subscription.LoadProfile(r.Context(), h.Source, r.PathValue("token"))
	switch {
	case errors.Is(err, panel.ErrNotFound):
		http.Error(w, "subscription not found", http.StatusNotFound)

		return
	case err != nil:
		logger.FromCtx(r.Context()).Error("subscription: load profile", "err", err) // the token is not logged
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

	format, ok := subscription.ChooseFormat(p.Settings, r.URL.Query().Get("format"), r.UserAgent())
	if !ok {
		http.Error(w, "subscription format is disabled", http.StatusNotFound)

		return
	}

	var (
		body        []byte
		contentType string
		ext         string
	)

	switch format {
	case subscription.FormatSingBox:
		body, err = subscription.SingBoxJSON(*p)
		contentType, ext = "application/json; charset=utf-8", "json"
	case subscription.FormatClash:
		body, err = subscription.ClashYAML(*p)
		contentType, ext = "text/yaml; charset=utf-8", "yaml"
	case subscription.FormatLinks:
		body, err = subscription.LinksBase64(*p)
		contentType, ext = "text/plain; charset=utf-8", "txt"
	default:
		body, err = subscription.LinksBase64(*p)
		contentType, ext = "text/plain; charset=utf-8", "txt"
	}

	var routing string
	if err == nil && format == subscription.FormatLinks && subscription.IsHapp(r.UserAgent()) {
		routing, err = subscription.HappRoutingLink()
	}

	if err != nil {
		logger.FromCtx(r.Context()).Error("subscription: render", "user_id", p.User.ID, "format", format, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	title := p.Settings.Title

	hdr := w.Header()
	hdr.Set("Content-Type", contentType)
	hdr.Set("Cache-Control", "no-store")
	hdr.Set("Subscription-Userinfo", p.UserInfo())
	hdr.Set("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(title)))
	hdr.Set("Profile-Update-Interval", strconv.Itoa(p.Settings.UpdateIntervalHours))
	hdr.Set("Content-Disposition", contentDisposition(title, ext))

	if routing != "" {
		hdr.Set("Routing", routing)
	}

	if v := p.Settings.SupportURL; v != "" {
		hdr.Set("Support-Url", v)
	}

	if v := p.Settings.ProfilePageURL; v != "" {
		hdr.Set("Profile-Web-Page-Url", v)
	}

	if v := p.Settings.Announce; v != "" {
		hdr.Set("Announce", "base64:"+base64.StdEncoding.EncodeToString([]byte(v)))
	}

	//nolint:gosec // body is a generated subscription document, not HTML; the content type is set above.
	_, _ = w.Write(body)
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}

	return time.Now()
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
