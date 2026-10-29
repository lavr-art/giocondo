package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/artemlavr/giocondo/internal/admin"
	"github.com/artemlavr/giocondo/internal/nodeapi/pki"
	"github.com/artemlavr/giocondo/internal/store"
)

type env struct {
	srv   *httptest.Server
	st    *store.SQLite
	svc   *admin.Service
	kicks *atomic.Int32
	now   time.Time
	token string // admin
}

func newEnv(t *testing.T) *env {
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
	e := &env{st: st, kicks: &atomic.Int32{}, now: time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)}
	svc := &admin.Service{Store: st, Kick: func() { e.kicks.Add(1) }, SubURL: "https://panel.example.com/",
		AgentAddr: "panel.example.com:9443", PKIDir: pkiDir, Now: func() time.Time { return e.now }}
	mux := http.NewServeMux()
	(&Handler{Admin: svc}).Register(mux)
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	e.svc = svc
	e.token = e.newToken(t, admin.ScopeAdmin)
	return e
}

func (e *env) newToken(t *testing.T, scope string) string {
	t.Helper()
	_, secret, err := e.svc.CreateToken(context.Background(), "test-"+scope, scope)
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func (e *env) do(t *testing.T, method, path, body string, out any) int {
	t.Helper()
	return e.doAs(t, e.token, method, path, body, out)
}

func (e *env) doAs(t *testing.T, token, method, path, body string, out any) int {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, e.srv.URL+path, r)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s %s: bad JSON %q: %v", method, path, data, err)
		}
	}
	return resp.StatusCode
}

func TestAuth(t *testing.T) {
	e := newEnv(t)
	for _, tok := range []string{"", "wrong", "vpc_wrong"} {
		if code := e.doAs(t, tok, "GET", "/api/users", "", nil); code != http.StatusUnauthorized {
			t.Errorf("token %q: %d", tok, code)
		}
	}
	// X-API-Key is no longer accepted.
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/users", nil)
	req.Header.Set("X-API-Key", e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("X-API-Key: %d", resp.StatusCode)
	}
}

func TestScopes(t *testing.T) {
	e := newEnv(t)
	mon, ns := e.newToken(t, admin.ScopeMonitor), e.newToken(t, admin.ScopeNodeSync)
	node := `{"name":"n1","host":"n1.example.com","hysteria2":{}}`
	tests := []struct {
		token, method, path, body string
		want                      int
	}{
		{mon, "GET", "/api/users", "", http.StatusOK},
		{mon, "GET", "/api/nodes", "", http.StatusOK},
		{mon, "POST", "/api/users", `{"name":"x"}`, http.StatusForbidden},
		{mon, "POST", "/api/nodes", node, http.StatusForbidden},
		{mon, "GET", "/api/tokens", "", http.StatusForbidden},
		{ns, "POST", "/api/nodes", node, http.StatusCreated},
		{ns, "GET", "/api/users", "", http.StatusOK},
		{ns, "POST", "/api/users", `{"name":"x"}`, http.StatusForbidden},
		{ns, "POST", "/api/tokens", `{"name":"t","scope":"admin"}`, http.StatusForbidden},
	}
	for _, tt := range tests {
		if code := e.doAs(t, tt.token, tt.method, tt.path, tt.body, nil); code != tt.want {
			t.Errorf("%s %s %s: %d, want %d", tt.token[:8], tt.method, tt.path, code, tt.want)
		}
	}
}

func TestTokensAPI(t *testing.T) {
	e := newEnv(t)
	var created struct {
		admin.TokenView
		Token string `json:"token"`
	}
	if code := e.do(t, "POST", "/api/tokens", `{"name":"grafana","scope":"monitor"}`, &created); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if !strings.HasPrefix(created.Token, "vpc_") || created.Scope != "monitor" {
		t.Fatalf("created %+v", created)
	}
	if code := e.doAs(t, created.Token, "GET", "/api/users", "", nil); code != http.StatusOK {
		t.Errorf("new token: %d", code)
	}
	if code := e.do(t, "POST", "/api/tokens", `{"name":"x","scope":"root"}`, nil); code != http.StatusBadRequest {
		t.Errorf("bad scope: %d", code)
	}

	var list []map[string]any
	e.do(t, "GET", "/api/tokens", "", &list)
	if len(list) != 2 {
		t.Fatalf("list %v", list)
	}
	for _, tok := range list {
		if _, ok := tok["token"]; ok {
			t.Error("token list exposes the secret")
		}
	}
	if code := e.do(t, "DELETE", "/api/tokens/"+itoa(created.ID), "", nil); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if code := e.doAs(t, created.Token, "GET", "/api/users", "", nil); code != http.StatusUnauthorized {
		t.Errorf("revoked token: %d", code)
	}
}

func TestUsersLifecycle(t *testing.T) {
	e := newEnv(t)
	var u admin.UserView
	if code := e.do(t, "POST", "/api/users", `{"name":"alice","traffic_limit_bytes":1000,"days":30}`, &u); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if u.Status != admin.StatusActive || !u.Enabled || u.ExpiresAt == nil || !u.ExpiresAt.Equal(e.now.AddDate(0, 0, 30)) {
		t.Errorf("created %+v", u)
	}
	if !strings.HasPrefix(u.SubscriptionURL, "https://panel.example.com/sub/") || len(u.SubscriptionURL) != len("https://panel.example.com/sub/")+43 {
		t.Errorf("subscription url %q", u.SubscriptionURL)
	}
	if e.kicks.Load() == 0 {
		t.Error("create did not kick nodes")
	}

	var errBody struct{ Error string }
	if code := e.do(t, "POST", "/api/users", `{"name":"alice"}`, &errBody); code != http.StatusConflict {
		t.Errorf("duplicate: %d", code)
	}
	for _, body := range []string{`{"name":"bad name"}`, `{"name":"x","days":-1}`, `{"name":"x","days":1,"expires_at":"2027-01-01T00:00:00Z"}`, `{"name":"x","bogus":1}`, `not json`} {
		if code := e.do(t, "POST", "/api/users", body, &errBody); code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, code)
		}
	}

	path := "/api/users/" + itoa(u.ID)
	// Traffic over the limit gives the limit status.
	n := store.Node{Name: "n1", Host: "h"}
	if err := e.st.CreateNode(context.Background(), &n); err != nil {
		t.Fatal(err)
	}
	if err := e.st.AddTraffic(context.Background(), []store.TrafficDelta{{UserID: u.ID, NodeID: n.ID, At: e.now, Up: 400, Down: 700}}); err != nil {
		t.Fatal(err)
	}
	e.do(t, "GET", path, "", &u)
	if u.Status != admin.StatusLimit || u.UsedBytes != 1100 {
		t.Errorf("over limit: %+v", u)
	}

	// Extension with a traffic reset.
	before := *u.ExpiresAt
	if code := e.do(t, "POST", path+"/extend", `{"days":30,"reset_traffic":true}`, &u); code != http.StatusOK {
		t.Fatalf("extend: %d", code)
	}
	if !u.ExpiresAt.Equal(before.AddDate(0, 0, 30)) || u.UsedBytes != 0 || u.Status != admin.StatusActive {
		t.Errorf("after extend %+v", u)
	}
	if code := e.do(t, "POST", path+"/extend", `{"days":0}`, &errBody); code != http.StatusBadRequest {
		t.Errorf("extend 0 days: %d", code)
	}

	// An expired term is extended from now, not from the old date.
	e.now = before.AddDate(0, 2, 0)
	e.do(t, "GET", path, "", &u)
	if u.Status != admin.StatusExpired {
		t.Errorf("status %s, want expired", u.Status)
	}
	e.do(t, "POST", path+"/extend", `{"days":10}`, &u)
	if !u.ExpiresAt.Equal(e.now.AddDate(0, 0, 10)) {
		t.Errorf("expired extend from now: %v", u.ExpiresAt)
	}

	// PATCH: disable, change the limit, remove the expiry.
	if code := e.do(t, "PATCH", path, `{"enabled":false,"traffic_limit_bytes":0,"clear_expiry":true}`, &u); code != http.StatusOK {
		t.Fatalf("patch: %d", code)
	}
	if u.Enabled || u.Status != admin.StatusDisabled || u.ExpiresAt != nil || u.TrafficLimitBytes != 0 {
		t.Errorf("after patch %+v", u)
	}
	if code := e.do(t, "POST", path+"/extend", `{"days":5}`, &errBody); code != http.StatusBadRequest || !strings.Contains(errBody.Error, "no expiry") {
		t.Errorf("extend unlimited: %d %q", code, errBody.Error)
	}

	oldURL := u.SubscriptionURL
	e.do(t, "POST", path+"/rotate-token", "", &u)
	if u.SubscriptionURL == oldURL {
		t.Error("token not rotated")
	}

	var list []admin.UserView
	e.do(t, "POST", "/api/users", `{"name":"Bob"}`, nil)
	if e.do(t, "GET", "/api/users", "", &list); len(list) != 2 || list[0].Name != "alice" || list[1].Name != "Bob" {
		t.Errorf("list %+v", list)
	}

	if code := e.do(t, "DELETE", path, "", nil); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	for _, p := range []string{path, "/api/users/999"} {
		if code := e.do(t, "GET", p, "", &errBody); code != http.StatusNotFound {
			t.Errorf("GET %s: %d", p, code)
		}
	}
	if code := e.do(t, "GET", "/api/users/abc", "", &errBody); code != http.StatusBadRequest {
		t.Errorf("bad id: %d", code)
	}
}

func TestNodesLifecycle(t *testing.T) {
	e := newEnv(t)
	// A user created before the node gets WG keys when a WG node appears.
	var u admin.UserView
	e.do(t, "POST", "/api/users", `{"name":"alice"}`, &u)
	if u.HasWireGuard {
		t.Error("WG keys without WG nodes")
	}

	var n admin.NodeView
	body := `{"name":"n1","host":"n1.example.com","region":"fsn1","vless_reality":{},"hysteria2":{"obfs":true},"wireguard":{"port":51821}}`
	if code := e.do(t, "POST", "/api/nodes", body, &n); code != http.StatusCreated {
		t.Fatalf("create node: %d", code)
	}
	if n.Status != "new" || len(n.Inbounds) != 3 || n.Inbounds[0].Port != 443 || n.Inbounds[1].Port != 8443 || n.Inbounds[2].Port != 51821 {
		t.Errorf("node %+v", n)
	}
	e.do(t, "GET", "/api/users/"+itoa(u.ID), "", &u)
	if !u.HasWireGuard {
		t.Error("user did not get WG keys after WG node was added")
	}
	// Secrets are generated and the config builds.
	ins, _ := e.st.ListInbounds(context.Background(), n.ID)
	if ins[0].Spec.Reality.PrivateKey == "" || ins[1].Spec.Hysteria2.CertPEM == "" || ins[1].Spec.Hysteria2.ObfsPassword == "" || ins[2].Spec.WireGuard.PrivateKey == "" {
		t.Errorf("secrets not generated: %+v", ins)
	}
	if ins[0].Spec.Reality.ServerName != "www.microsoft.com" {
		t.Errorf("default sni %q", ins[0].Spec.Reality.ServerName)
	}

	var errBody struct{ Error string }
	for body, want := range map[string]int{
		`{"name":"n1","host":"x","wireguard":{}}`:                              http.StatusConflict,
		`{"name":"n2","host":"x"}`:                                             http.StatusBadRequest, // no protocols
		`{"name":"n2","host":""}`:                                              http.StatusBadRequest,
		`{"name":"bad/name","host":"x","wireguard":{}}`:                        http.StatusBadRequest,
		`{"name":"n2","host":"203.0.113.1","hysteria2":{"acme":true}}`:         http.StatusBadRequest, // ACME on an IP
		`{"name":"n2","host":"x","vless_reality":{},"hysteria2":{"port":443}}`: http.StatusCreated,    // tcp/443 and udp/443 do not conflict
	} {
		if code := e.do(t, "POST", "/api/nodes", body, &errBody); code != want {
			t.Errorf("%s: %d, want %d (%s)", body, code, want, errBody.Error)
		}
	}

	path := "/api/nodes/" + itoa(n.ID)
	kicks := e.kicks.Load()
	if code := e.do(t, "PATCH", path, `{"disabled":true,"ip":"203.0.113.10"}`, &n); code != http.StatusOK || n.Status != "disabled" || n.IP != "203.0.113.10" {
		t.Errorf("disable: %d %+v", code, n)
	}
	if e.kicks.Load() == kicks {
		t.Error("node update did not kick")
	}
	if e.do(t, "PATCH", path, `{"disabled":false}`, &n); n.Status != "offline" {
		t.Errorf("enable: %s", n.Status)
	}

	var cert admin.AgentCert
	if code := e.do(t, "POST", path+"/agent-cert", "", &cert); code != http.StatusOK {
		t.Fatalf("agent cert: %d", code)
	}
	if !strings.Contains(cert.AgentCert, "BEGIN CERTIFICATE") || !strings.Contains(cert.AgentKey, "PRIVATE KEY") || cert.PanelAddr != "panel.example.com:9443" {
		t.Errorf("cert %+v", cert)
	}

	var list []admin.NodeView
	if e.do(t, "GET", "/api/nodes", "", &list); len(list) != 2 {
		t.Errorf("nodes %d", len(list))
	}
	if code := e.do(t, "DELETE", path, "", nil); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if code := e.do(t, "GET", path, "", &errBody); code != http.StatusNotFound {
		t.Errorf("deleted node: %d", code)
	}
}

// Every route is described in docs/api.yaml and vice versa.
func TestOpenAPIMatchesRoutes(t *testing.T) {
	data, err := os.ReadFile("../../docs/api.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("docs/api.yaml: %v", err)
	}
	inSpec := map[string]bool{}
	for path, ops := range spec.Paths {
		for method := range ops {
			if method != "parameters" {
				inSpec[strings.ToUpper(method)+" "+path] = true
			}
		}
	}
	inCode := map[string]bool{}
	for _, r := range Routes {
		inCode[r.Method+" "+r.Path] = true
		if !inSpec[r.Method+" "+r.Path] {
			t.Errorf("%s %s is not documented in docs/api.yaml", r.Method, r.Path)
		}
	}
	for k := range inSpec {
		if !inCode[k] {
			t.Errorf("docs/api.yaml documents %s, but there is no such route", k)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestHosts(t *testing.T) {
	e := newEnv(t)
	var n admin.NodeView
	e.do(t, "POST", "/api/nodes", `{"name":"n1","host":"n1.example.com","vless_reality":{},"hysteria2":{},"wireguard":{}}`, &n)
	base := "/api/nodes/" + itoa(n.ID) + "/hosts"

	var h admin.HostView
	if code := e.do(t, "POST", base, `{"inbound":"hy2","address":"198.51.100.7","port":9443,"sni":"cdn.example.net","sort":2}`, &h); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if h.InboundTag != "hy2" || h.Port != 9443 || h.SNI != "cdn.example.net" {
		t.Errorf("host %+v", h)
	}
	var errBody struct{ Error string }
	for body, want := range map[string]string{
		`{"inbound":"nope","address":"1.2.3.4"}`:                    "no inbound",
		`{"inbound":"vless","address":"bad host!"}`:                 "domain or IP",
		`{"inbound":"vless","address":"1.2.3.4","sni":"other.com"}`: "Reality accepts only",
		`{"inbound":"wg","address":"1.2.3.4","sni":"x.com"}`:        "no SNI",
	} {
		if code := e.do(t, "POST", base, body, &errBody); code != http.StatusBadRequest || !strings.Contains(errBody.Error, want) {
			t.Errorf("%s: %d %q", body, code, errBody.Error)
		}
	}
	// An SNI equal to the node server_name is allowed for Reality.
	if code := e.do(t, "POST", base, `{"inbound":"vless","address":"203.0.113.10","sni":"www.microsoft.com"}`, nil); code != http.StatusCreated {
		t.Errorf("reality with own sni: %d", code)
	}

	var list []admin.HostView
	if e.do(t, "GET", base, "", &list); len(list) != 2 || list[0].InboundTag != "vless" {
		t.Errorf("list %+v", list)
	}
	var node admin.NodeView
	if e.do(t, "GET", "/api/nodes/"+itoa(n.ID), "", &node); len(node.Hosts) != 2 {
		t.Errorf("node hosts %+v", node.Hosts)
	}

	path := "/api/hosts/" + itoa(h.ID)
	if code := e.do(t, "PUT", path, `{"address":"198.51.100.8","sort":1}`, &h); code != http.StatusOK || h.Address != "198.51.100.8" || h.SNI != "" || h.InboundTag != "hy2" {
		t.Errorf("update: %d %+v", code, h)
	}
	if code := e.do(t, "DELETE", path, "", nil); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	if code := e.do(t, "DELETE", path, "", &errBody); code != http.StatusNotFound {
		t.Errorf("delete twice: %d", code)
	}
	if code := e.do(t, "GET", "/api/nodes/999/hosts", "", &errBody); code != http.StatusNotFound {
		t.Errorf("missing node: %d", code)
	}
}

func TestProvisioningAPI(t *testing.T) {
	e := newEnv(t)
	var n admin.NodeView
	if code := e.do(t, "POST", "/api/nodes", `{"name":"n1","host":"n1.example.com","hysteria2":{}}`, &n); code != http.StatusCreated {
		t.Fatalf("create node: %d", code)
	}
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/nodes/"+itoa(n.ID)+"/cloud-init", nil)
	req.Header.Set("Authorization", "Bearer "+e.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/cloud-config") ||
		!strings.HasPrefix(string(body), "#cloud-config") {
		t.Fatalf("cloud-init: %d %s\n%s", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}

	mon := e.newToken(t, admin.ScopeMonitor)
	var key struct {
		AuthorizedKey string `json:"authorized_key"`
	}
	if code := e.doAs(t, mon, "GET", "/api/ssh-key", "", &key); code != http.StatusOK || !strings.HasPrefix(key.AuthorizedKey, "ssh-ed25519 ") {
		t.Errorf("ssh-key: %d %+v", code, key)
	}
	if code := e.doAs(t, mon, "POST", "/api/nodes/"+itoa(n.ID)+"/provision-ssh", `{}`, nil); code != http.StatusForbidden {
		t.Errorf("monitor provision: %d", code)
	}
	var job struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	// Port 1 on loopback is closed: the job starts and fails quickly.
	if code := e.do(t, "POST", "/api/nodes/"+itoa(n.ID)+"/provision-ssh", `{"host":"127.0.0.1","port":1,"password":"x"}`, &job); code != http.StatusAccepted || job.ID == "" {
		t.Fatalf("provision: %d %+v", code, job)
	}
	deadline := time.Now().Add(10 * time.Second)
	for job.Status != "failed" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		e.do(t, "GET", "/api/jobs/"+job.ID, "", &job)
	}
	if job.Status != "failed" {
		t.Errorf("job status %q", job.Status)
	}
	if code := e.do(t, "GET", "/api/jobs/unknown", "", nil); code != http.StatusNotFound {
		t.Errorf("unknown job: %d", code)
	}
}
