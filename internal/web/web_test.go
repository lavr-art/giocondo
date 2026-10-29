package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"image/png"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/admin"
	"github.com/artemlavr/giocondo/internal/nodeapi/pki"
	"github.com/artemlavr/giocondo/internal/store"
)

const testPassword = "correct horse"

type env struct {
	srv *httptest.Server
	svc *admin.Service
	st  *store.SQLite
	h   *Handler
}

func newEnv(t *testing.T, password string) *env {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	pkiDir := filepath.Join(dir, "pki")
	if err := pki.InitCA(pkiDir); err != nil {
		t.Fatal(err)
	}
	svc := &admin.Service{Store: st, SubURL: "https://panel.example.com", AgentAddr: "panel.example.com:9443", PKIDir: pkiDir}
	h, err := New(Config{Admin: svc, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &env{srv: srv, svc: svc, st: st, h: h}
}

// client is a browser: it keeps cookies, does not follow redirects itself and sends its site's Origin.
type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func (e *env) client(t *testing.T) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: e.srv.URL, http: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

func (c *client) do(method, path string, form url.Values, hdr map[string]string) (*http.Response, string) {
	c.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, _ := http.NewRequest(method, c.base+path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if method != http.MethodGet {
		req.Header.Set("Origin", c.base)
	}
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	return resp, string(data)
}

func (c *client) login() {
	c.t.Helper()
	resp, _ := c.do("POST", "/login", url.Values{"password": {testPassword}, "next": {"/nodes"}}, nil)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/nodes" {
		c.t.Fatalf("login: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestLogin(t *testing.T) {
	e := newEnv(t, testPassword)
	c := e.client(t)

	resp, _ := c.do("GET", "/users", nil, nil)
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Fatalf("unauthenticated: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, _ = c.do("GET", "/users", nil, map[string]string{"HX-Request": "true"})
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("HX-Redirect") != "/login" {
		t.Errorf("htmx unauthenticated: %d", resp.StatusCode)
	}
	if resp, body := c.do("GET", "/login", nil, nil); resp.StatusCode != http.StatusOK || !strings.Contains(body, `type="password"`) {
		t.Errorf("login page: %d", resp.StatusCode)
	}

	if resp, body := c.do("POST", "/login", url.Values{"password": {"wrong"}}, nil); resp.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "Wrong password") {
		t.Errorf("wrong password: %d", resp.StatusCode)
	}
	// A login from another site is rejected.
	if resp, _ := c.do("POST", "/login", url.Values{"password": {testPassword}}, map[string]string{"Origin": "https://evil.example"}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin login: %d", resp.StatusCode)
	}
	// An open redirect through next does not work.
	resp, _ = c.do("POST", "/login", url.Values{"password": {testPassword}, "next": {"//evil.example/x"}}, nil)
	if resp.Header.Get("Location") != "/users" {
		t.Errorf("open redirect: %s", resp.Header.Get("Location"))
	}
	ck := resp.Cookies()
	if len(ck) != 1 || !ck[0].HttpOnly || ck[0].SameSite != http.SameSiteStrictMode {
		t.Errorf("session cookie %+v", ck)
	}

	resp, body := c.do("GET", "/users", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Users") {
		t.Fatalf("users after login: %d", resp.StatusCode)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("csp %q", csp)
	}
	if resp, _ := c.do("POST", "/logout", nil, nil); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("logout: %d", resp.StatusCode)
	}
	if resp, _ := c.do("GET", "/users", nil, nil); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("after logout: %d", resp.StatusCode)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t, testPassword)
	c := e.client(t)
	for range loginMaxFails {
		c.do("POST", "/login", url.Values{"password": {"nope"}}, nil)
	}
	// Even the right password is not accepted until the window passes.
	if resp, _ := c.do("POST", "/login", url.Values{"password": {testPassword}}, nil); resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("after %d failures: %d", loginMaxFails, resp.StatusCode)
	}
	e.h.limit.now = func() time.Time { return time.Now().Add(loginWindow + time.Second) }
	if resp, _ := c.do("POST", "/login", url.Values{"password": {testPassword}}, nil); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("after window: %d", resp.StatusCode)
	}
}

func TestSessionExpiryAndTampering(t *testing.T) {
	s := newSessions("secret", true, "/")
	rec := httptest.NewRecorder()
	s.issue(rec)
	cookie := rec.Result().Cookies()[0]
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookie)
	if !s.valid(req) {
		t.Fatal("fresh session invalid")
	}
	if !cookie.Secure {
		t.Error("cookie not Secure behind https")
	}
	other := newSessions("other secret", true, "/")
	if other.valid(req) {
		t.Error("session valid with another secret")
	}
	tampered := httptest.NewRequest("GET", "/", nil)
	exp, sig, _ := strings.Cut(cookie.Value, ".")
	tampered.AddCookie(&http.Cookie{Name: sessionCookie, Value: exp + "9." + sig})
	if s.valid(tampered) {
		t.Error("tampered expiry accepted")
	}
	s.now = func() time.Time { return time.Now().Add(sessionTTL + time.Minute) }
	if s.valid(req) {
		t.Error("expired session accepted")
	}
}

func TestDisabledWithoutPassword(t *testing.T) {
	e := newEnv(t, "")
	c := e.client(t)
	for _, p := range []string{"/login", "/users"} {
		if resp, _ := c.do("GET", p, nil, nil); resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
	if resp, _ := c.do("POST", "/login", url.Values{"password": {""}}, nil); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("empty password login: %d", resp.StatusCode)
	}
}

func TestUsersFlow(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, testPassword)
	c := e.client(t)
	c.login()

	// A WireGuard node so the user gets WG keys and a config.
	resp, _ := c.do("POST", "/nodes", url.Values{"name": {"n1"}, "host": {"n1.example.com"}, "vless": {"on"}, "wg": {"on"}, "wg_port": {"51820"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create node: %d", resp.StatusCode)
	}
	nodes, _ := e.svc.ListNodes(ctx)

	resp, _ = c.do("POST", "/users", url.Values{"name": {"alice"}, "limit_gb": {"1,5"}, "days": {"30"}}, nil)
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/users/") {
		t.Fatalf("create user: %d", resp.StatusCode)
	}
	userPath := resp.Header.Get("Location")
	users, _ := e.svc.ListUsers(ctx)
	u := users[0]
	if u.TrafficLimitBytes != 3<<29 || u.ExpiresAt == nil || !u.HasWireGuard {
		t.Errorf("created %+v", u)
	}

	// A state-changing request without Origin and Sec-Fetch-Site is rejected (CSRF).
	if resp, _ := c.do("POST", "/users", url.Values{"name": {"bob"}}, map[string]string{"Origin": ""}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("no origin: %d", resp.StatusCode)
	}
	if resp, _ := c.do("POST", "/users", url.Values{"name": {"bob"}}, map[string]string{"Origin": "", "Sec-Fetch-Site": "same-origin"}); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("sec-fetch-site same-origin: %d", resp.StatusCode)
	}

	_, body := c.do("GET", userPath, nil, nil)
	for _, want := range []string{u.SubscriptionURL, "/qr.png", "n1.conf", "Extend"} {
		if !strings.Contains(body, want) {
			t.Errorf("user page has no %q", want)
		}
	}
	if strings.Contains(body, "<script>") {
		t.Error("inline script on page (CSP)")
	}

	resp, body = c.do("GET", userPath+"/qr.png", nil, nil)
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("qr content type %q", resp.Header.Get("Content-Type"))
	}
	if _, err := png.Decode(strings.NewReader(body)); err != nil {
		t.Errorf("qr is not png: %v", err)
	}

	_, body = c.do("GET", userPath+"/wg/"+itoa(nodes[0].ID), nil, nil)
	if !strings.Contains(body, "[Interface]") || !strings.Contains(body, "Endpoint = n1.example.com:51820") {
		t.Errorf("wg conf: %q", body)
	}

	// An htmx action from the table returns a row.
	resp, body = c.do("POST", userPath+"/disable", url.Values{"from": {"list"}}, map[string]string{"HX-Request": "true"})
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(strings.TrimSpace(body), "<tr id=\"user-") || !strings.Contains(body, "disabled") {
		t.Errorf("htmx disable: %d %q", resp.StatusCode, body)
	}
	// A regular form from the user page redirects with a message.
	resp, _ = c.do("POST", userPath+"/extend", url.Values{"days": {"10"}, "reset_traffic": {"on"}}, nil)
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "flash=") {
		t.Errorf("extend: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	got, _ := e.svc.GetUser(ctx, u.ID)
	if !got.ExpiresAt.Equal(u.ExpiresAt.AddDate(0, 0, 10)) {
		t.Errorf("expires %v, want %v", got.ExpiresAt, u.ExpiresAt.AddDate(0, 0, 10))
	}
	c.do("POST", userPath+"/limit", url.Values{"limit_gb": {""}}, nil)
	c.do("POST", userPath+"/expiry", url.Values{"expires": {""}}, nil)
	if got, _ = e.svc.GetUser(ctx, u.ID); got.TrafficLimitBytes != 0 || got.ExpiresAt != nil {
		t.Errorf("limit/expiry not cleared: %+v", got)
	}

	// An error in an htmx request comes in HX-Trigger, not as a page.
	resp, _ = c.do("POST", userPath+"/extend", url.Values{"from": {"list"}, "days": {"30"}}, map[string]string{"HX-Request": "true"})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(resp.Header.Get("HX-Trigger"), "showError") || resp.Header.Get("HX-Reswap") != "none" {
		t.Errorf("htmx error: %d %v", resp.StatusCode, resp.Header)
	}
	// A regular form error.
	if resp, body := c.do("POST", "/users", url.Values{"name": {"alice"}}, nil); resp.StatusCode != http.StatusConflict || !strings.Contains(body, "already taken") {
		t.Errorf("duplicate: %d %q", resp.StatusCode, body)
	}

	_, body = c.do("GET", "/users", nil, nil)
	if !strings.Contains(body, `id="user-`) || !strings.Contains(body, "alice") {
		t.Error("users table")
	}
	resp, body = c.do("POST", userPath+"/delete", nil, map[string]string{"HX-Request": "true"})
	if resp.StatusCode != http.StatusOK || body != "" {
		t.Errorf("htmx delete: %d %q", resp.StatusCode, body)
	}
	if resp, _ := c.do("GET", userPath, nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("deleted user page: %d", resp.StatusCode)
	}
	if resp, _ := c.do("POST", "/users/1/bogus", nil, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown action: %d", resp.StatusCode)
	}
}

func TestNodesFlow(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, testPassword)
	c := e.client(t)
	c.login()

	if resp, body := c.do("POST", "/nodes", url.Values{"name": {"n1"}, "host": {"1.2.3.4"}}, nil); resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "protocol") {
		t.Errorf("no protocols: %d %q", resp.StatusCode, body)
	}
	if resp, _ := c.do("POST", "/nodes", url.Values{"name": {"n1"}, "host": {"x"}, "wg": {"on"}, "wg_port": {"99999"}}, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad port: %d", resp.StatusCode)
	}
	resp, _ := c.do("POST", "/nodes", url.Values{"name": {"n1"}, "host": {"n1.example.com"}, "region": {"fsn1"},
		"vless": {"on"}, "hy2": {"on"}, "hy2_obfs": {"on"}, "wg": {"on"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	nodePath, _, _ := strings.Cut(resp.Header.Get("Location"), "?")

	_, body := c.do("GET", "/nodes", nil, nil)
	for _, want := range []string{"n1", "VLESS Reality", "Hysteria2", "WireGuard", "waiting for agent"} {
		if !strings.Contains(body, want) {
			t.Errorf("nodes page has no %q", want)
		}
	}
	_, body = c.do("GET", nodePath, nil, nil)
	if !strings.Contains(body, "--panel panel.example.com:9443") {
		t.Error("install command without panel address")
	}

	resp, body = c.do("POST", nodePath+"/bundle", nil, nil)
	if resp.Header.Get("Content-Type") != "application/gzip" || !strings.Contains(resp.Header.Get("Content-Disposition"), "n1-agent.tar.gz") {
		t.Fatalf("bundle headers %v", resp.Header)
	}
	files := untar(t, []byte(body))
	for _, name := range []string{"n1/ca.crt", "n1/agent.crt", "n1/agent.key", "n1/README.txt", "n1/install-agent.sh"} {
		if _, ok := files[name]; !ok {
			t.Errorf("bundle has no %s (has %v)", name, keys(files))
		}
	}
	if files["n1/agent.key"].mode != 0o600 || files["n1/install-agent.sh"].mode != 0o755 {
		t.Errorf("modes: key %o, script %o", files["n1/agent.key"].mode, files["n1/install-agent.sh"].mode)
	}
	if readme := files["n1/README.txt"].body; !strings.Contains(readme, "--panel panel.example.com:9443") ||
		!strings.Contains(readme, "--repo "+admin.DefaultAptRepo) {
		t.Errorf("README without panel address or apt repo:\n%s", readme)
	}

	c.do("POST", nodePath+"/update", url.Values{"host": {"n1b.example.com"}, "ip": {"203.0.113.9"}, "region": {"hel1"}}, nil)
	c.do("POST", nodePath+"/disable", nil, nil)
	nodes, _ := e.svc.ListNodes(ctx)
	if nodes[0].Host != "n1b.example.com" || nodes[0].Status != string(store.NodeDisabled) {
		t.Errorf("after update/disable %+v", nodes[0])
	}
	if resp, _ := c.do("POST", nodePath+"/delete", nil, nil); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("delete: %d", resp.StatusCode)
	}
	if nodes, _ := e.svc.ListNodes(ctx); len(nodes) != 0 {
		t.Error("node not deleted")
	}
}

func TestStatic(t *testing.T) {
	e := newEnv(t, testPassword)
	c := e.client(t)
	for _, p := range []string{"/static/htmx.min.js", "/static/app.js", "/static/style.css"} {
		resp, body := c.do("GET", p, nil, nil)
		if resp.StatusCode != http.StatusOK || len(body) == 0 {
			t.Errorf("%s: %d", p, resp.StatusCode)
		}
	}
}

func TestHelpers(t *testing.T) {
	for in, want := range map[string]int64{"": 0, "1": 1 << 30, "1.5": 3 << 29, "0,5": 1 << 29} {
		if got, err := parseGB(in); err != nil || got != want {
			t.Errorf("parseGB(%q) = %d, %v", in, got, err)
		}
	}
	for _, bad := range []string{"-1", "abc", "NaN", "1e9"} {
		if _, err := parseGB(bad); err == nil {
			t.Errorf("parseGB(%q) accepted", bad)
		}
	}
	if humanBytes(512) != "512 B" || humanBytes(1536) != "1.5 KB" || humanBytes(5<<30) != "5.0 GB" {
		t.Errorf("humanBytes: %s %s %s", humanBytes(512), humanBytes(1536), humanBytes(5<<30))
	}
	if percent(50, 200) != 25 || percent(500, 100) != 100 || percent(1, 0) != 0 {
		t.Error("percent")
	}
	for in, want := range map[string]string{"/nodes": "/nodes", "": "/users", "//evil": "/users", "https://evil": "/users", "/\\evil": "/users"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %q", in, got)
		}
	}
}

type tarFile struct {
	mode int64
	body string
}

func untar(t *testing.T, data []byte) map[string]tarFile {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string]tarFile{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		out[h.Name] = tarFile{mode: h.Mode, body: string(b)}
	}
}

func keys(m map[string]tarFile) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestHostsFlow(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, testPassword)
	c := e.client(t)
	c.login()
	resp, _ := c.do("POST", "/nodes", url.Values{"name": {"n1"}, "host": {"n1.example.com"}, "vless": {"on"}, "hy2": {"on"}}, nil)
	nodePath, _, _ := strings.Cut(resp.Header.Get("Location"), "?")

	resp, _ = c.do("POST", nodePath+"/hosts", url.Values{"inbound": {"hy2"}, "address": {"198.51.100.7"}, "port": {"9443"}, "sni": {"cdn.example.net"}}, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("add host: %d", resp.StatusCode)
	}
	if resp, body := c.do("POST", nodePath+"/hosts", url.Values{"inbound": {"vless"}, "address": {"1.2.3.4"}, "sni": {"evil.com"}}, nil); resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "Reality") {
		t.Errorf("reality sni: %d %q", resp.StatusCode, body)
	}
	_, body := c.do("GET", nodePath, nil, nil)
	if !strings.Contains(body, "198.51.100.7") || !strings.Contains(body, "cdn.example.net") || !strings.Contains(body, `<option value="vless">`) {
		t.Error("host not shown on node page")
	}
	nodes, _ := e.svc.ListNodes(ctx)
	hostID := nodes[0].Hosts[0].ID
	resp, _ = c.do("POST", "/hosts/"+itoa(hostID)+"/delete", nil, nil)
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), nodePath+"?") {
		t.Errorf("delete host: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if nodes, _ = e.svc.ListNodes(ctx); len(nodes[0].Hosts) != 0 {
		t.Error("host not deleted")
	}
}
