// Package web is the panel web interface: html/template pages and htmx actions.
// Templates and static files are embedded in the binary. Login uses the PANEL_ADMIN_PASSWORD password.
package web

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rsc.io/qr"

	"github.com/artemlavr/giocondo/internal/admin"
	"github.com/artemlavr/giocondo/internal/provision"
	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Handler is the web interface.
type Handler struct {
	admin    *admin.Service
	password string
	sess     *sessions
	limit    *limiter
	pages    map[string]*template.Template
	log      *slog.Logger
	base     string // admin prefix (PANEL_ADMIN_PATH) or ""
}

// Config holds the web interface settings.
type Config struct {
	Admin         *admin.Service
	Password      string // empty disables the interface
	SessionSecret string // empty means random per start
	SecureCookie  bool   // the panel is served over HTTPS
	// Base is the path the admin UI is mounted under (PANEL_ADMIN_PATH), e.g. "/a1b2c3"; "" means the root.
	// Routes are registered without it (the mux sits behind http.StripPrefix), while links, redirects and
	// cookies get it added.
	Base string
	Log  *slog.Logger
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
		admin: cfg.Admin, password: cfg.Password, sess: newSessions(cfg.SessionSecret, cfg.SecureCookie, cookiePath),
		limit: &limiter{now: time.Now}, log: cfg.Log, pages: map[string]*template.Template{}, base: base,
	}
	if h.log == nil {
		h.log = slog.Default()
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
	for _, page := range []string{"login", "users", "user", "nodes", "node", "tokens"} {
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
	mux.HandleFunc("GET /{$}", h.auth(func(w http.ResponseWriter, r *http.Request) { h.redirect(w, r, "/users", http.StatusSeeOther) }))

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
}

// redirect is http.Redirect to a path inside the admin UI (with the Base prefix).
func (h *Handler) redirect(w http.ResponseWriter, r *http.Request, path string, code int) {
	http.Redirect(w, r, h.base+path, code)
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
				w.Header().Set("HX-Redirect", h.base+"/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h.redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && !sameOrigin(r) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'")
		next(w, r)
	}
}

func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// page is the page data.
type page struct {
	Title  string
	Nav    string
	Flash  string
	Error  string
	Data   any
	Authed bool
}

func (h *Handler) render(w http.ResponseWriter, name string, p page, status int) {
	var buf bytes.Buffer
	if err := h.pages[name].ExecuteTemplate(&buf, "layout.html", p); err != nil {
		h.log.Error("render", "page", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// renderPartial renders one template block as the response to an htmx request.
func (h *Handler) renderPartial(w http.ResponseWriter, page, block string, data any) {
	var buf bytes.Buffer
	if err := h.pages[page].ExecuteTemplate(&buf, block, data); err != nil {
		h.log.Error("render partial", "block", block, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

// errText returns a readable operation error text and the response code.
func (h *Handler) errText(err error) (string, int) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return "Not found", http.StatusNotFound
	case errors.Is(err, store.ErrConflict):
		return "This name is already taken", http.StatusConflict
	case errors.Is(err, admin.ErrInvalid):
		return strings.TrimPrefix(err.Error(), admin.ErrInvalid.Error()+": "), http.StatusBadRequest
	}
	h.log.Error("web", "err", err)
	return "Internal error, see the panel log for details", http.StatusInternalServerError
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	msg, code := h.errText(err)
	if isHTMX(r) {
		// By default htmx does not swap content on 4xx/5xx: show the text through an alert.
		w.Header().Set("HX-Reswap", "none")
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showError": %q}`, msg))
		w.WriteHeader(code)
		return
	}
	http.Error(w, msg, code)
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: bad id", admin.ErrInvalid)
	}
	return id, nil
}

// --- login ---

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	if h.password == "" {
		http.Error(w, "web interface is disabled: set PANEL_ADMIN_PASSWORD", http.StatusServiceUnavailable)
		return
	}
	if h.sess.valid(r) {
		h.redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	h.render(w, "login", page{Title: "Sign in", Data: safeNext(r.URL.Query().Get("next"))}, http.StatusOK)
}

// safeNext allows only local paths so ?next= cannot lead to another site.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") {
		return "/users"
	}
	return next
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if h.password == "" {
		http.Error(w, "web interface is disabled: set PANEL_ADMIN_PASSWORD", http.StatusServiceUnavailable)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "cross-origin request rejected", http.StatusForbidden)
		return
	}
	ip := clientIP(r)
	next := safeNext(r.FormValue("next"))
	if h.limit.blocked(ip) {
		h.render(w, "login", page{Title: "Sign in", Error: "Too many attempts, wait 10 minutes", Data: next}, http.StatusTooManyRequests)
		return
	}
	if !checkPassword(r.FormValue("password"), h.password) {
		h.limit.fail(ip)
		h.log.Warn("failed login", "ip", ip)
		h.render(w, "login", page{Title: "Sign in", Error: "Wrong password", Data: next}, http.StatusUnauthorized)
		return
	}
	h.limit.reset(ip)
	h.sess.issue(w)
	h.redirect(w, r, next, http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	h.sess.clear(w)
	h.redirect(w, r, "/login", http.StatusSeeOther)
}

// --- users ---

type usersData struct {
	Users []admin.UserView
}

func (h *Handler) usersPage(w http.ResponseWriter, r *http.Request) {
	users, err := h.admin.ListUsers(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, "users", page{Title: "Users", Nav: "users", Authed: true, Flash: r.URL.Query().Get("flash"), Data: usersData{Users: users}}, http.StatusOK)
}

// parseGB converts "100" or "1.5" GB into bytes; empty means 0 (no limit).
func parseGB(s string) (int64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" {
		return 0, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 || v > 1e6 || math.IsNaN(v) {
		return 0, fmt.Errorf("%w: the GB limit must be a number from 0", admin.ErrInvalid)
	}
	return int64(v * (1 << 30)), nil
}

func parseDays(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	d, err := strconv.Atoi(s)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("%w: days must be an integer from 0", admin.ErrInvalid)
	}
	return d, nil
}

func (h *Handler) createUser(w http.ResponseWriter, r *http.Request) {
	limit, err := parseGB(r.FormValue("limit_gb"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	days, err := parseDays(r.FormValue("days"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	u, err := h.admin.CreateUser(r.Context(), admin.CreateUserInput{Name: r.FormValue("name"), TrafficLimitBytes: limit, Days: days})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, fmt.Sprintf("/users/%d", u.ID), http.StatusSeeOther)
}

type userData struct {
	User    admin.UserView
	WGNodes []admin.NodeView
}

func (h *Handler) userPage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	u, err := h.admin.GetUser(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	nodes, err := h.admin.ListNodes(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := userData{User: u}
	if u.HasWireGuard {
		for _, n := range nodes {
			for _, in := range n.Inbounds {
				if in.Type == singbox.TypeWireGuard && n.Status != string(store.NodeDisabled) {
					d.WGNodes = append(d.WGNodes, n)
					break
				}
			}
		}
	}
	h.render(w, "user", page{Title: u.Name, Nav: "users", Authed: true, Flash: r.URL.Query().Get("flash"), Data: d}, http.StatusOK)
}

func (h *Handler) userQR(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	u, err := h.admin.GetUser(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	code, err := qr.Encode(u.SubscriptionURL, qr.M)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	code.Scale = 8
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(code.PNG())
}

// userWG serves the user's wg-quick config for a node (for clients without a subscription).
func (h *Handler) userWG(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	nodeID, err := strconv.ParseInt(r.PathValue("node"), 10, 64)
	if err != nil {
		h.fail(w, r, fmt.Errorf("%w: bad node id", admin.ErrInvalid))
		return
	}
	u, err := h.admin.UserRecord(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	n, err := h.admin.Store.GetNode(r.Context(), nodeID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ins, err := h.admin.Store.ListInbounds(r.Context(), nodeID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	spec := store.SingboxNode(*n, ins)
	conf, err := singbox.WireGuardClientConfig(&spec, u.SingboxUser())
	if err != nil {
		h.fail(w, r, fmt.Errorf("%w: %w", admin.ErrInvalid, err))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.conf"`, u.Name, n.Name))
	_, _ = w.Write([]byte(conf))
}

// userAction handles actions from the table and the user page. An htmx request gets
// the updated table row, otherwise it redirects back to the user page.
func (h *Handler) userAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ctx := r.Context()
	var (
		u     admin.UserView
		flash string
	)
	switch r.PathValue("action") {
	case "extend":
		days, perr := parseDays(r.FormValue("days"))
		if perr != nil {
			h.fail(w, r, perr)
			return
		}
		u, err = h.admin.Extend(ctx, id, days)
		if err == nil && r.FormValue("reset_traffic") == "on" {
			u, err = h.admin.ResetTraffic(ctx, id)
		}
		flash = "Term extended"
	case "limit":
		limit, perr := parseGB(r.FormValue("limit_gb"))
		if perr != nil {
			h.fail(w, r, perr)
			return
		}
		u, err = h.admin.UpdateUser(ctx, id, admin.UpdateUserInput{TrafficLimitBytes: &limit})
		flash = "Limit changed"
	case "expiry":
		in := admin.UpdateUserInput{ClearExpiry: strings.TrimSpace(r.FormValue("expires")) == ""}
		if !in.ClearExpiry {
			t, perr := time.Parse("2006-01-02", r.FormValue("expires"))
			if perr != nil {
				h.fail(w, r, fmt.Errorf("%w: date must be YYYY-MM-DD", admin.ErrInvalid))
				return
			}
			in.ExpiresAt = &t
		}
		u, err = h.admin.UpdateUser(ctx, id, in)
		flash = "Term changed"
	case "enable", "disable":
		on := r.PathValue("action") == "enable"
		u, err = h.admin.UpdateUser(ctx, id, admin.UpdateUserInput{Enabled: &on})
		flash = map[bool]string{true: "User enabled", false: "User disabled"}[on]
	case "reset-traffic":
		u, err = h.admin.ResetTraffic(ctx, id)
		flash = "Traffic reset"
	case "rotate-token":
		u, err = h.admin.RotateToken(ctx, id)
		flash = "New link issued, the old one no longer works"
	case "delete":
		if err := h.admin.DeleteUser(ctx, id); err != nil {
			h.fail(w, r, err)
			return
		}
		if isHTMX(r) {
			w.WriteHeader(http.StatusOK) // empty response: htmx removes the row (hx-swap=delete)
			return
		}
		h.redirect(w, r, "/users?flash="+url.QueryEscape("User deleted"), http.StatusSeeOther)
		return
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if isHTMX(r) && r.FormValue("from") == "list" {
		h.renderPartial(w, "users", "user_row", u)
		return
	}
	h.redirect(w, r, fmt.Sprintf("/users/%d?flash=%s", id, url.QueryEscape(flash)), http.StatusSeeOther)
}

// --- nodes ---

type nodesData struct {
	Nodes []admin.NodeView
}

func (h *Handler) nodesPage(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.admin.ListNodes(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.render(w, "nodes", page{Title: "Nodes", Nav: "nodes", Authed: true, Flash: r.URL.Query().Get("flash"), Data: nodesData{Nodes: nodes}}, http.StatusOK)
}

func formPort(r *http.Request, key string) (uint16, error) {
	s := strings.TrimSpace(r.FormValue(key))
	if s == "" {
		return 0, nil
	}
	p, err := strconv.ParseUint(s, 10, 16)
	if err != nil || p == 0 {
		return 0, fmt.Errorf("%w: port must be a number from 1 to 65535", admin.ErrInvalid)
	}
	return uint16(p), nil
}

func (h *Handler) createNode(w http.ResponseWriter, r *http.Request) {
	in := admin.CreateNodeInput{Name: r.FormValue("name"), Host: r.FormValue("host"), IP: r.FormValue("ip"), Region: r.FormValue("region")}
	var err error
	port := func(key string) uint16 {
		if err != nil {
			return 0
		}
		var p uint16
		p, err = formPort(r, key)
		return p
	}
	if r.FormValue("vless") == "on" {
		in.VLESS = &admin.VLESSInput{Port: port("vless_port"), Dest: strings.TrimSpace(r.FormValue("vless_dest"))}
	}
	if r.FormValue("hy2") == "on" {
		in.Hysteria2 = &admin.Hy2Input{Port: port("hy2_port"), ACME: r.FormValue("hy2_acme") == "on",
			ACMEEmail: strings.TrimSpace(r.FormValue("hy2_email")), Obfs: r.FormValue("hy2_obfs") == "on"}
	}
	if r.FormValue("wg") == "on" {
		in.WireGuard = &admin.WGNodeInput{Port: port("wg_port")}
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	n, err := h.admin.CreateNode(r.Context(), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, fmt.Sprintf("/nodes/%d?flash=%s", n.ID, url.QueryEscape("Node added. Install the agent on it.")), http.StatusSeeOther)
}

type nodeData struct {
	Node      admin.NodeView
	AgentAddr string
	AptRepo   string
	DeployKey string             // panel public SSH key
	Job       *provision.JobView // installation job from ?job=
}

func (h *Handler) nodePage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	n, err := h.admin.GetNode(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := nodeData{Node: n, AgentAddr: h.admin.AgentAddr, AptRepo: h.admin.AptRepoURL()}
	if n.Runtime != string(store.RuntimeLocal) {
		if key, err := h.admin.DeployKey(); err == nil {
			d.DeployKey = key.AuthorizedKey
		} else {
			h.log.Warn("deploy key", "err", err)
		}
	}
	if jobID := r.URL.Query().Get("job"); jobID != "" {
		if j, err := h.admin.Job(jobID); err == nil && j.NodeID == n.ID {
			d.Job = &j
		}
	}
	h.render(w, "node", page{Title: n.Name, Nav: "nodes", Authed: true, Flash: r.URL.Query().Get("flash"), Data: d}, http.StatusOK)
}

func (h *Handler) nodeCloudInit(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data, name, err := h.admin.CloudInit(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/cloud-config; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	_, _ = w.Write(data)
}

func (h *Handler) nodeProvisionSSH(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	in := admin.SSHInput{
		Host: r.FormValue("host"), User: r.FormValue("user"), Password: r.FormValue("password"),
		PrivateKey: r.FormValue("private_key"), HostKeySHA256: r.FormValue("host_key"),
		NoFirewall: r.FormValue("firewall") == "", KeepKey: r.FormValue("remove_key") == "",
	}
	if p := strings.TrimSpace(r.FormValue("port")); p != "" {
		if in.Port, err = strconv.Atoi(p); err != nil {
			h.fail(w, r, fmt.Errorf("%w: port must be a number", admin.ErrInvalid))
			return
		}
	}
	job, err := h.admin.ProvisionSSH(r.Context(), id, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, fmt.Sprintf("/nodes/%d?job=%s", id, url.QueryEscape(job.ID)), http.StatusSeeOther)
}

// jobPartial is the job block polled by htmx (every 2 s while the job runs).
func (h *Handler) jobPartial(w http.ResponseWriter, r *http.Request) {
	j, err := h.admin.Job(r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, "node", "job", j)
}

func (h *Handler) nodeBundle(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data, name, err := h.admin.AgentBundle(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	_, _ = w.Write(data)
}

func (h *Handler) nodeAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var flash string
	switch r.PathValue("action") {
	case "add-address", "delete-address", "enable-dns", "rotate-ip":
		h.nodeNetwork(w, r)
		return
	case "enable", "disable":
		off := r.PathValue("action") == "disable"
		_, err = h.admin.UpdateNode(r.Context(), id, admin.UpdateNodeInput{Disabled: &off})
		flash = map[bool]string{true: "Node disabled and removed from subscriptions", false: "Node enabled"}[off]
	case "update":
		host, ip, region := r.FormValue("host"), r.FormValue("ip"), r.FormValue("region")
		_, err = h.admin.UpdateNode(r.Context(), id, admin.UpdateNodeInput{Host: &host, IP: &ip, Region: &region})
		flash = "Saved"
	case "delete":
		if err := h.admin.DeleteNode(r.Context(), id); err != nil {
			h.fail(w, r, err)
			return
		}
		h.redirect(w, r, "/nodes?flash="+url.QueryEscape("Node removed from the panel"), http.StatusSeeOther)
		return
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, fmt.Sprintf("/nodes/%d?flash=%s", id, url.QueryEscape(flash)), http.StatusSeeOther)
}

func (h *Handler) createHost(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	port, err := formPort(r, "port")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	sort, err := parseDays(r.FormValue("sort")) // an integer from 0
	if err != nil {
		h.fail(w, r, fmt.Errorf("%w: order must be an integer from 0", admin.ErrInvalid))
		return
	}
	_, err = h.admin.CreateHost(r.Context(), id, admin.HostInput{Inbound: r.FormValue("inbound"), Address: r.FormValue("address"),
		Port: port, SNI: r.FormValue("sni"), Sort: sort})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, fmt.Sprintf("/nodes/%d?flash=%s", id, url.QueryEscape("Address added to the subscription")), http.StatusSeeOther)
}

func (h *Handler) deleteHost(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	nodeID, err := h.admin.DeleteHost(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, fmt.Sprintf("/nodes/%d?flash=%s", nodeID, url.QueryEscape("Address removed from the subscription")), http.StatusSeeOther)
}

// --- formatting ---

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	v := float64(b) / unit
	i := 0
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[i]
}

func fmtDate(t *time.Time) string {
	if t == nil {
		return "no expiry"
	}
	return t.UTC().Format("2006-01-02")
}

func ago(t *time.Time) string {
	if t == nil {
		return "never"
	}
	d := time.Since(*t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d d ago", int(d.Hours()/24))
}

func percent(used, limit int64) int {
	if limit <= 0 {
		return 0
	}
	return int(min(100, used*100/limit))
}

func statusLabel(s string) string {
	switch s {
	case admin.StatusActive:
		return "active"
	case admin.StatusDisabled:
		return "disabled"
	case admin.StatusExpired:
		return "expired"
	case admin.StatusLimit:
		return "out of traffic"
	case "online":
		return "online"
	case "offline":
		return "offline"
	case "new":
		return "waiting for agent"
	}
	return s
}

func protoLabel(t singbox.InboundType) string {
	switch t {
	case singbox.TypeVLESSReality:
		return "VLESS Reality"
	case singbox.TypeHysteria2:
		return "Hysteria2"
	case singbox.TypeWireGuard:
		return "WireGuard"
	}
	return string(t)
}

// --- API tokens ---

type tokensData struct {
	Tokens  []admin.TokenView
	Created *admin.TokenView // just issued: its secret is shown once
	Secret  string
}

func (h *Handler) tokensPage(w http.ResponseWriter, r *http.Request) {
	h.renderTokens(w, r, tokensData{}, http.StatusOK)
}

func (h *Handler) renderTokens(w http.ResponseWriter, r *http.Request, d tokensData, status int) {
	list, err := h.admin.ListTokens(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d.Tokens = list
	h.render(w, "tokens", page{Title: "API tokens", Nav: "tokens", Authed: true, Flash: r.URL.Query().Get("flash"), Data: d}, status)
}

func (h *Handler) createToken(w http.ResponseWriter, r *http.Request) {
	t, secret, err := h.admin.CreateToken(r.Context(), r.FormValue("name"), r.FormValue("scope"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// No redirect: the secret exists only in this response.
	h.renderTokens(w, r, tokensData{Created: &t, Secret: secret}, http.StatusOK)
}

func (h *Handler) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.admin.DeleteToken(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, "/tokens?flash="+url.QueryEscape("Token revoked"), http.StatusSeeOther)
}
