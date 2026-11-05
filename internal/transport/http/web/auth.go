package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/artemlavr/giocondo/pkg/logger"
)

const (
	sessionCookie = "giocondo_vpn_session"
	sessionTTL    = 7 * 24 * time.Hour

	loginWindow   = 10 * time.Minute
	loginMaxFails = 10
)

// sessions are signed cookies with no server-side storage: "<expires unix>.<hmac>".
// The secret comes from PANEL_SESSION_SECRET or is random per start (then sessions are reset
// when the panel restarts).
type sessions struct {
	secret []byte
	secure bool
	path   string // cookie Path: the admin prefix or "/"
	now    func() time.Time
}

func newSessions(secret string, secure bool, path string) *sessions {
	s := &sessions{secure: secure, path: path, now: time.Now}

	if secret != "" {
		sum := sha256.Sum256([]byte(secret))
		s.secret = sum[:]
	} else {
		s.secret = make([]byte, 32)
		_, _ = rand.Read(s.secret)
	}

	return s
}

func (s *sessions) sign(payload string) string {
	m := hmac.New(sha256.New, s.secret)
	m.Write([]byte(payload))

	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *sessions) issue(w http.ResponseWriter) {
	exp := strconv.FormatInt(s.now().Add(sessionTTL).Unix(), 10)

	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is enabled whenever the panel is served over TLS.
		Name: sessionCookie, Value: exp + "." + s.sign(exp), Path: s.path,
		MaxAge: int(sessionTTL.Seconds()), HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode,
	})
}

func (s *sessions) clear(w http.ResponseWriter) {
	//nolint:gosec // Secure is enabled whenever the panel is served over TLS.
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: s.path, MaxAge: -1,
		HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode,
	})
}

func (s *sessions) valid(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}

	exp, sig, ok := strings.Cut(c.Value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.sign(exp))) {
		return false
	}

	unix, err := strconv.ParseInt(exp, 10, 64)

	return err == nil && s.now().Before(time.Unix(unix, 0))
}

func checkPassword(got, want string) bool {
	// Compare hashes so the password length does not leak through comparison timing.
	a, b := sha256.Sum256([]byte(got)), sha256.Sum256([]byte(want))

	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

// limiter limits failed login attempts from one address.
type limiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
	now   func() time.Time
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}

func (l *limiter) recent(ip string) []time.Time {
	cut := l.now().Add(-loginWindow)

	var keep []time.Time

	for _, t := range l.fails[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}

	return keep
}

func (l *limiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.recent(ip)) >= loginMaxFails
}

func (l *limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.fails == nil {
		l.fails = map[string][]time.Time{}
	}

	l.fails[ip] = append(l.recent(ip), l.now())
}

func (l *limiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.fails, ip)
}

// sameOrigin is CSRF protection for state-changing requests (in addition to SameSite=Strict):
// Origin (or Referer) must point to the same host; without both headers only
// Sec-Fetch-Site: same-origin is trusted.
func sameOrigin(r *http.Request) bool {
	for _, h := range []string{"Origin", "Referer"} {
		v := r.Header.Get(h)
		if v == "" {
			continue
		}

		u, err := url.Parse(v)

		return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
	}

	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	if h.password == "" {
		http.Error(w, "web interface is disabled: set PANEL_ADMIN_PASSWORD", http.StatusServiceUnavailable)

		return
	}

	if h.sess.valid(r) {
		h.redirect(w, r, "/users")

		return
	}

	h.render(w, r, "login", page{Title: loginTitle, Data: safeNext(r.URL.Query().Get("next"))}, http.StatusOK)
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
		h.render(
			w, r,
			"login",
			page{Title: loginTitle, Error: "Too many attempts, wait 10 minutes", Data: next},
			http.StatusTooManyRequests,
		)

		return
	}

	if !checkPassword(r.FormValue("password"), h.password) {
		h.limit.fail(ip)
		logger.FromCtx(r.Context()).Warn("failed login", "ip", ip)
		h.render(w, r, "login", page{Title: loginTitle, Error: "Wrong password", Data: next}, http.StatusUnauthorized)

		return
	}

	h.limit.reset(ip)
	h.sess.issue(w)
	h.redirect(w, r, next)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	h.sess.clear(w)
	h.redirect(w, r, "/login")
}
