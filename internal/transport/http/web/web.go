// Package web is the panel web interface: html/template pages and htmx actions.
// Templates and static files are embedded in the binary. Login uses the PANEL_ADMIN_PASSWORD password.
package web

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/pkg/logger"
)

const (
	loginTitle = "Sign in"
	// Template and navigation names of the list pages.
	pageUsers = "users"
	pageNodes = "nodes"

	actionEnable  = "enable"
	actionDisable = "disable"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Handler is the web interface.
type Handler struct {
	Dependencies

	password string
	sess     *sessions
	limit    *limiter
	pages    map[string]*template.Template
	base     string // admin prefix (PANEL_ADMIN_PATH) or ""
}

// Config holds the web interface settings.
type Config struct {
	Dependencies

	Password      string // empty disables the interface
	SessionSecret string // empty means random per start
	SecureCookie  bool   // the panel is served over HTTPS
	// Base is the path the admin UI is mounted under (PANEL_ADMIN_PATH), e.g. "/a1b2c3"; "" means the root.
	// Routes are registered without it (the mux sits behind http.StripPrefix), while links, redirects and
	// cookies get it added.
	Base string
}

// New builds the interface and parses the templates.
func New(cfg Config) (*Handler, error) {
	base := strings.TrimRight(cfg.Base, "/")
	if base != "" && !strings.HasPrefix(base, "/") {
		base = "/" + base
	}

	cookiePath := base
	if cookiePath == "" {
		cookiePath = "/"
	}

	h := &Handler{
		Dependencies: cfg.Dependencies, password: cfg.Password, sess: newSessions(
			cfg.SessionSecret,
			cfg.SecureCookie,
			cookiePath,
		),
		limit: &limiter{now: time.Now}, pages: map[string]*template.Template{}, base: base,
	}

	funcs := template.FuncMap{
		"bytes":    humanBytes,
		"gb":       func(b int64) string { return strconv.FormatFloat(float64(b)/(1<<30), 'f', -1, 64) },
		"date":     fmtDate,
		"datetime": func(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") + " UTC" },
		"ago":      ago,
		"percent":  percent,
		"status":   statusLabel,
		"proto":    protoLabel,
		"base":     func() string { return base },
	}
	for _, page := range []string{"login", pageUsers, "user", pageNodes, "node", "tokens", "settings"} {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(templatesFS,
			"templates/layout.html", "templates/partials.html", "templates/"+page+".html")
		if err != nil {
			return nil, fmt.Errorf("template %s: %w", page, err)
		}

		h.pages[page] = t
	}

	return h, nil
}

// Register attaches the pages to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheForever(http.FileServerFS(static))))
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.login)
	mux.HandleFunc("POST /logout", h.auth(h.logout))
	mux.HandleFunc("GET /{$}", h.auth(func(w http.ResponseWriter, r *http.Request) { h.redirect(w, r, "/users") }))

	mux.HandleFunc("GET /users", h.auth(h.usersPage))
	mux.HandleFunc("POST /users", h.auth(h.createUser))
	mux.HandleFunc("GET /users/{id}", h.auth(h.userPage))
	mux.HandleFunc("GET /users/{id}/qr.png", h.auth(h.userQR))
	mux.HandleFunc("GET /users/{id}/wg/{node}", h.auth(h.userWG))
	mux.HandleFunc("POST /users/{id}/{action}", h.auth(h.userAction))

	mux.HandleFunc("GET /nodes", h.auth(h.nodesPage))
	mux.HandleFunc("POST /nodes", h.auth(h.createNode))
	mux.HandleFunc("GET /nodes/{id}", h.auth(h.nodePage))
	mux.HandleFunc("POST /nodes/{id}/bundle", h.auth(h.nodeBundle))
	mux.HandleFunc("POST /nodes/{id}/cloud-init", h.auth(h.nodeCloudInit))
	mux.HandleFunc("POST /nodes/{id}/provision-ssh", h.auth(h.nodeProvisionSSH))
	mux.HandleFunc("GET /jobs/{id}", h.auth(h.jobPartial))
	mux.HandleFunc("POST /jobs/{id}/{action}", h.auth(h.resumeNetwork))
	mux.HandleFunc("POST /nodes/{id}/hosts", h.auth(h.createHost))
	mux.HandleFunc("POST /hosts/{id}/delete", h.auth(h.deleteHost))
	mux.HandleFunc("POST /nodes/{id}/{action}", h.auth(h.nodeAction))

	mux.HandleFunc("GET /tokens", h.auth(h.tokensPage))
	mux.HandleFunc("POST /tokens", h.auth(h.createToken))
	mux.HandleFunc("POST /tokens/{id}/delete", h.auth(h.deleteToken))

	mux.HandleFunc("GET /settings", h.auth(h.settingsPage))
	mux.HandleFunc("POST /settings", h.auth(h.updateSettings))
}

// redirect is http.Redirect to a path inside the admin UI (with the Base prefix).
func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, h.base+path, http.StatusSeeOther)
}

func cacheForever(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.password == "" {
			http.Error(w, "web interface is disabled: set PANEL_ADMIN_PASSWORD", http.StatusServiceUnavailable)

			return
		}

		if !h.sess.valid(r) {
			if isHTMX(r) {
				w.Header().Set("Hx-Redirect", h.base+"/login")
				w.WriteHeader(http.StatusUnauthorized)

				return
			}

			h.redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path))

			return
		}

		if r.Method != http.MethodGet && !sameOrigin(r) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'",
		)
		next(w, r)
	}
}

func isHTMX(r *http.Request) bool { return r.Header.Get("Hx-Request") == "true" }

// page is the page data.
type page struct {
	Title  string
	Nav    string
	Flash  string
	Error  string
	Data   any
	Authed bool
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, name string, p page, status int) {
	var buf bytes.Buffer
	if err := h.pages[name].ExecuteTemplate(&buf, "layout.html", p); err != nil {
		logger.FromCtx(r.Context()).Error("render", "page", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// renderPartial renders one template block as the response to an htmx request.
func (h *Handler) renderPartial(w http.ResponseWriter, r *http.Request, page, block string, data any) {
	var buf bytes.Buffer
	if err := h.pages[page].ExecuteTemplate(&buf, block, data); err != nil {
		logger.FromCtx(r.Context()).Error("render partial", "block", block, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

// errText returns a readable operation error text and the response code.
func (h *Handler) errText(ctx context.Context, err error) (string, int) {
	switch {
	case errors.Is(err, panel.ErrNotFound):
		return "Not found", http.StatusNotFound
	case errors.Is(err, panel.ErrConflict):
		return "This name is already taken", http.StatusConflict
	case errors.Is(err, panel.ErrInvalid):
		return strings.TrimPrefix(err.Error(), panel.ErrInvalid.Error()+": "), http.StatusBadRequest
	}

	logger.FromCtx(ctx).Error("web", "err", err)

	return "Internal error, see the panel log for details", http.StatusInternalServerError
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	msg, code := h.errText(r.Context(), err)

	if isHTMX(r) {
		// By default htmx does not swap content on 4xx/5xx: show the text through an alert.
		w.Header().Set("Hx-Reswap", "none")
		w.Header().Set("Hx-Trigger", fmt.Sprintf(`{"showError": %q}`, msg))
		w.WriteHeader(code)

		return
	}

	http.Error(w, msg, code)
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: bad id", panel.ErrInvalid)
	}

	return id, nil
}
