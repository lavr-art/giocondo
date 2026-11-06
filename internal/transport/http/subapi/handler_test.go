package subapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	yaml "gopkg.in/yaml.v3"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
	"github.com/artemlavr/giocondo/internal/repository/sqlite/hosts"
	"github.com/artemlavr/giocondo/internal/repository/sqlite/nodes"
	"github.com/artemlavr/giocondo/internal/repository/sqlite/traffic"
	"github.com/artemlavr/giocondo/internal/repository/sqlite/users"
	"github.com/artemlavr/giocondo/internal/service/subscription"
)

const fixtureDir = "../../../service/subscription/testdata"

type nodeFile struct {
	Node  protocol.Node   `yaml:"node"`
	Users []protocol.User `yaml:"users"`
}

func loadNodeFile(t *testing.T, name string) nodeFile {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(fixtureDir, name))
	if err != nil {
		t.Fatal(err)
	}

	var f nodeFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}

	return f
}

type fixture struct {
	users *users.Repository
	nodes *nodes.Repository
	hosts *hosts.Repository
	user  panel.User
	srv   *httptest.Server
}

// newFixture stores two nodes (n2 disabled) and user alice in a real database and serves the handler.
func newFixture(t *testing.T) *fixture {
	t.Helper()

	ctx := context.Background()

	db, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	f := &fixture{users: users.New(db), nodes: nodes.New(db), hosts: hosts.New(db)}

	n1, n2 := loadNodeFile(t, "n1.yaml"), loadNodeFile(t, "n2.yaml")

	var firstNodeID int64

	for i, nf := range []nodeFile{n1, n2} {
		n := panel.Node{Name: nf.Node.ID, Host: nf.Node.Host}
		if i == 1 {
			n.Status = panel.NodeDisabled
		}

		if err := f.nodes.CreateNode(ctx, &n); err != nil {
			t.Fatal(err)
		}

		if i == 0 {
			firstNodeID = n.ID
		}

		for _, in := range nf.Node.Inbounds {
			if err := f.nodes.CreateInbound(ctx, &panel.Inbound{NodeID: n.ID, Spec: in}); err != nil {
				t.Fatal(err)
			}
		}
	}

	var alice protocol.User

	for _, u := range n1.Users {
		if u.Name == "alice" {
			alice = u
		}
	}

	exp := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	f.user = panel.User{
		Name: alice.Name, UUID: alice.UUID, Enabled: true, TrafficLimitBytes: 100 << 30, ExpiresAt: &exp, WG: alice.WG,
	}

	if err := f.users.CreateUser(ctx, &f.user); err != nil {
		t.Fatal(err)
	}

	deltas := []panel.TrafficDelta{{UserID: f.user.ID, NodeID: firstNodeID, At: time.Now(), Up: 100, Down: 200}}
	if err := traffic.New(db).AddTraffic(ctx, deltas); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	h := &Handler{
		Source: subscription.Source{Users: f.users, Nodes: f.nodes, Hosts: f.hosts},
		Title:  "Mön VPN",
		Now:    func() time.Time { return time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC) },
	}
	h.Register(mux)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)

	return f
}

func get(t *testing.T, url, userAgent string) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	return resp, string(body)
}

func TestHandlerFormats(t *testing.T) {
	f := newFixture(t)
	base := f.srv.URL + "/sub/" + f.user.Token

	resp, body := get(t, base, "v2rayNG/1.10.5")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}

	h := resp.Header
	if got := h.Get("Subscription-Userinfo"); got != "upload=100; download=200; total=107374182400; expire=1798761600" {
		t.Errorf("userinfo %q", got)
	}

	if got := h.Get("Profile-Title"); got != "base64:"+base64.StdEncoding.EncodeToString([]byte("Mön VPN")) {
		t.Errorf("profile-title %q", got)
	}

	if h.Get("Profile-Update-Interval") != "12" || h.Get("Cache-Control") != "no-store" {
		t.Errorf("headers %v", h)
	}

	cd := h.Get("Content-Disposition")
	if !strings.Contains(cd, `filename="M_n VPN.txt"`) || !strings.Contains(cd, "filename*=UTF-8''M%C3%B6n%20VPN.txt") {
		t.Errorf("content-disposition %q", cd)
	}

	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("body is not base64: %v", err)
	}

	// Only n1 (n2 is disabled): vless, hy2, wg.
	links := strings.Split(string(decoded), "\n")
	if len(links) != 3 || !strings.HasPrefix(links[0], "vless://") || !strings.HasPrefix(links[2], "wireguard://") {
		t.Errorf("links %v", links)
	}

	if strings.Contains(string(decoded), "n2.example.com") {
		t.Error("disabled node in subscription")
	}

	resp, body = get(t, base, "SFA/1.12.4 (Android 14; sing-box 1.12.4)")

	var cfg map[string]any
	if err := json.Unmarshal([]byte(body), &cfg); err != nil || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Errorf("sing-box format: %v, %s", err, resp.Header.Get("Content-Type"))
	}

	if resp.Header.Get("Subscription-Userinfo") == "" {
		t.Error("no userinfo for json")
	}

	resp, body = get(t, base, "clash-verge/v2.2.3")
	if !strings.Contains(body, "proxy-groups:") || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/yaml") {
		t.Errorf("clash format: %s", resp.Header.Get("Content-Type"))
	}

	// A host override from the database replaces the node address in the link.
	ctx := context.Background()

	all, err := f.nodes.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}

	ins, err := f.nodes.ListInbounds(ctx, all[0].ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := f.hosts.CreateHost(ctx, &panel.Host{InboundID: ins[0].ID, Address: "203.0.113.99", Port: 8443}); err != nil {
		t.Fatal(err)
	}

	_, body = get(t, base, "v2rayNG/1.10.5")

	decoded, err = base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatal(err)
	}

	first := strings.Split(string(decoded), "\n")[0]
	if !strings.Contains(string(decoded), "@203.0.113.99:8443?") || strings.Contains(first, "n1.example.com:443") {
		t.Errorf("host override not applied: %s", decoded)
	}

	// ?format= wins over User-Agent.
	_, body = get(t, base+"?format=clash", "v2rayNG/1.10.5")
	if !strings.Contains(body, "proxy-groups:") {
		t.Error("?format=clash ignored")
	}
}

func TestHandlerErrors(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	if resp, _ := get(t, f.srv.URL+"/sub/unknown", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown token: %d", resp.StatusCode)
	}

	if resp, _ := get(t, f.srv.URL+"/sub/", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("empty token: %d", resp.StatusCode)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.srv.URL+"/sub/"+f.user.Token, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", resp.StatusCode)
	}

	cases := []struct {
		name     string
		mutate   func(*panel.User)
		status   int
		userinfo string
	}{
		{"disabled", func(u *panel.User) { u.Enabled = false }, http.StatusForbidden, ""},
		// Expired term / exhausted traffic: the subscription is served so the client shows the reason.
		{
			"expired",
			func(u *panel.User) { e := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC); u.ExpiresAt = &e },
			http.StatusOK,
			"upload=100; download=200; total=107374182400; expire=1793059200",
		},
		{
			"traffic limit reached",
			func(u *panel.User) { u.TrafficLimitBytes = 300 },
			http.StatusOK,
			"upload=100; download=200; total=300; expire=1798761600",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cur, err := f.users.GetUser(ctx, f.user.ID)
			if err != nil {
				t.Fatal(err)
			}

			orig := *cur

			c.mutate(cur)

			if err := f.users.UpdateUser(ctx, cur); err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() {
				if err := f.users.UpdateUser(ctx, &orig); err != nil {
					t.Error(err)
				}
			})

			resp, body := get(t, f.srv.URL+"/sub/"+f.user.Token, "")
			if resp.StatusCode != c.status {
				t.Errorf("status %d, body %q", resp.StatusCode, body)
			}

			if c.status == http.StatusForbidden && !strings.Contains(body, c.name) {
				t.Errorf("body %q", body)
			}

			if got := resp.Header.Get("Subscription-Userinfo"); c.userinfo != "" && got != c.userinfo {
				t.Errorf("userinfo %q, want %q", got, c.userinfo)
			}
		})
	}
}

func TestHandlerHappRouting(t *testing.T) {
	const happ = "Happ/4.7.2/macOS/2610301208604"

	f := newFixture(t)
	base := f.srv.URL + "/sub/" + f.user.Token

	want, err := subscription.HappRoutingLink()
	if err != nil {
		t.Fatal(err)
	}

	_, plainBody := get(t, base, "curl/8.5.0")

	resp, body := get(t, base, happ)
	if got := resp.Header.Get("Routing"); got != want {
		t.Errorf("routing %q, want %q", got, want)
	}

	if body != plainBody {
		t.Error("Happ body differs from the plain links body")
	}

	if resp, _ = get(t, base+"?format=links", happ); resp.Header.Get("Routing") != want {
		t.Errorf("?format=links: routing %q", resp.Header.Get("Routing"))
	}

	withoutRouting := []struct {
		name      string
		url       string
		userAgent string
	}{
		{"v2RayTun reads another routing format", base, "v2RayTun/2.0"},
		{"Hiddify", base, "HiddifyNext/4.0.0(darwin) like ClashMeta v2ray sing-box"},
		{"unknown client", base, "curl/8.5.0"},
		{"links format for another client", base + "?format=links", "v2rayNG/2.3.15"},
		{"Happ asking for sing-box", base + "?format=singbox", happ},
		{"Happ asking for clash", base + "?format=clash", happ},
		{"unknown token", f.srv.URL + "/sub/unknown", happ},
	}
	for _, tt := range withoutRouting {
		t.Run(tt.name, func(t *testing.T) {
			resp, _ := get(t, tt.url, tt.userAgent)
			if got := resp.Header.Get("Routing"); got != "" {
				t.Errorf("unexpected routing %q", got)
			}
		})
	}
}

func TestHandlerHappRoutingDisabledUser(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	cur, err := f.users.GetUser(ctx, f.user.ID)
	if err != nil {
		t.Fatal(err)
	}

	cur.Enabled = false
	if err := f.users.UpdateUser(ctx, cur); err != nil {
		t.Fatal(err)
	}

	resp, _ := get(t, f.srv.URL+"/sub/"+f.user.Token, "Happ/4.7.2/macOS/2610301208604")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status %d", resp.StatusCode)
	}

	if got := resp.Header.Get("Routing"); got != "" {
		t.Errorf("disabled user got routing %q", got)
	}
}
