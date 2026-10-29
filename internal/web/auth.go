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
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: exp + "." + s.sign(exp), Path: s.path,
		MaxAge: int(sessionTTL.Seconds()), HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode,
	})
}

func (s *sessions) clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: s.path, MaxAge: -1, HttpOnly: true, Secure: s.secure, SameSite: http.SameSiteStrictMode})
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
