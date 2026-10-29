package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/store"
)

func TestDetectFormat(t *testing.T) {
	tests := []struct {
		ua   string
		want Format
	}{
		{"HiddifyNext/2.5.7 (android) like ClashMeta v2ray sing-box", FormatLinks},
		{"Hiddify/3.0.0 (ios)", FormatLinks},
		{"Happ/1.9.2", FormatLinks},
		{"v2rayNG/1.10.5", FormatLinks},
		{"v2rayN/7.12.5", FormatLinks},
		{"Streisand/1.6", FormatLinks},
		{"V2Box 2.0 (iOS)", FormatLinks},
		{"Shadowrocket/2.2.58 CFNetwork/1568.100.1 Darwin/24.0.0", FormatLinks},
		{"SFA/1.12.4 (Android 14; sing-box 1.12.4; language en)", FormatSingBox},
		{"SFI/1.11.0 (iOS 17.0; sing-box 1.11.0)", FormatSingBox},
		{"sing-box 1.14.4", FormatSingBox},
		{"Karing/1.1.2", FormatSingBox},
		{"clash-verge/v2.2.3", FormatClash},
		{"ClashMetaForAndroid/2.11.1.Meta", FormatClash},
		{"FlClash/v0.8.80", FormatClash},
		{"mihomo/1.19.0", FormatClash},
		{"Stash/2.4.7 Clash/1.9.0", FormatClash},
		{"curl/8.5.0", FormatLinks},
		{"", FormatLinks},
	}
	for _, tt := range tests {
		if got := DetectFormat(tt.ua); got != tt.want {
			t.Errorf("DetectFormat(%q) = %s, want %s", tt.ua, got, tt.want)
		}
	}
}

// seedStore creates a database with two fixture nodes (n2 disabled) and user alice.
func seedStore(t *testing.T) (*store.SQLite, *store.User) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	f := fixture(t, "alice")
	for i, np := range f.Nodes {
		n := np.Node
		if i == 1 {
			n.Status = store.NodeDisabled
		}
		if err := s.CreateNode(ctx, &n); err != nil {
			t.Fatal(err)
		}
		for _, in := range np.Inbounds {
			if err := s.CreateInbound(ctx, &store.Inbound{NodeID: n.ID, Spec: in}); err != nil {
				t.Fatal(err)
			}
		}
	}
	u := f.User
	u.ID, u.Token = 0, ""
	if err := s.CreateUser(ctx, &u); err != nil {
		t.Fatal(err)
	}
	nodes, _ := s.ListNodes(ctx)
	if err := s.AddTraffic(ctx, []store.TrafficDelta{{UserID: u.ID, NodeID: nodes[0].ID, At: time.Now(), Up: 100, Down: 200}}); err != nil {
		t.Fatal(err)
	}
	return s, &u
}

func newServer(t *testing.T, s store.Store) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	h := &Handler{Source: s, Title: "Mön VPN", Now: func() time.Time { return time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC) }}
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url, ua string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", ua)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestHandlerFormats(t *testing.T) {
	s, u := seedStore(t)
	srv := newServer(t, s)
	base := srv.URL + "/sub/" + u.Token

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
	if cd := h.Get("Content-Disposition"); !strings.Contains(cd, `filename="M_n VPN.txt"`) || !strings.Contains(cd, "filename*=UTF-8''M%C3%B6n%20VPN.txt") {
		t.Errorf("content-disposition %q", cd)
	}
	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("body is not base64: %v", err)
	}
	links := strings.Split(string(decoded), "\n")
	// Only n1 (n2 is disabled): vless, hy2, wg.
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
	ins, _ := s.ListInbounds(context.Background(), 1)
	if err := s.CreateHost(context.Background(), &store.Host{InboundID: ins[0].ID, Address: "203.0.113.99", Port: 8443}); err != nil {
		t.Fatal(err)
	}
	_, body = get(t, base, "v2rayNG/1.10.5")
	decoded, _ = base64.StdEncoding.DecodeString(body)
	if !strings.Contains(string(decoded), "@203.0.113.99:8443?") || strings.Contains(strings.Split(string(decoded), "\n")[0], "n1.example.com:443") {
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
	s, u := seedStore(t)
	srv := newServer(t, s)

	if resp, _ := get(t, srv.URL+"/sub/unknown", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown token: %d", resp.StatusCode)
	}
	if resp, _ := get(t, srv.URL+"/sub/", ""); resp.StatusCode != http.StatusNotFound {
		t.Errorf("empty token: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/sub/"+u.Token, nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %v %v", resp.StatusCode, err)
	}

	cases := []struct {
		name     string
		mutate   func(*store.User)
		status   int
		userinfo string
	}{
		{"disabled", func(u *store.User) { u.Enabled = false }, http.StatusForbidden, ""},
		// Expired term / exhausted traffic: the subscription is served so the client shows the reason.
		{"expired", func(u *store.User) { e := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC); u.ExpiresAt = &e },
			http.StatusOK, "upload=100; download=200; total=107374182400; expire=1793059200"},
		{"traffic limit reached", func(u *store.User) { u.TrafficLimitBytes = 300 },
			http.StatusOK, "upload=100; download=200; total=300; expire=1798761600"},
	}
	for _, c := range cases {
		cur, err := s.GetUser(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		orig := *cur
		c.mutate(cur)
		if err := s.UpdateUser(ctx, cur); err != nil {
			t.Fatal(err)
		}
		resp, body := get(t, srv.URL+"/sub/"+u.Token, "")
		if resp.StatusCode != c.status {
			t.Errorf("%s: %d %q", c.name, resp.StatusCode, body)
		}
		if c.status == http.StatusForbidden && !strings.Contains(body, c.name) {
			t.Errorf("%s: body %q", c.name, body)
		}
		if got := resp.Header.Get("Subscription-Userinfo"); c.userinfo != "" && got != c.userinfo {
			t.Errorf("%s: userinfo %q, want %q", c.name, got, c.userinfo)
		}
		if err := s.UpdateUser(ctx, &orig); err != nil {
			t.Fatal(err)
		}
	}
}
