package panelintegration

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/subscription"
	"github.com/artemlavr/giocondo/internal/transport/http/subapi"
	"github.com/artemlavr/giocondo/internal/transport/http/web"
	. "github.com/artemlavr/giocondo/test/paneltest" //nolint:revive // dot import keeps the test package call sites concise.
)

func TestSubscriptionSettingsSources(t *testing.T) {
	ctx := context.Background()

	t.Run("built-in defaults", func(t *testing.T) {
		s, _ := newService(t, newClock(time.Now()))

		got, err := s.Settings.SubscriptionSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if got != panel.DefaultSubscriptionSettings() {
			t.Fatalf("settings = %+v", got)
		}
	})

	t.Run("environment overrides the default, the database overrides the environment", func(t *testing.T) {
		s, _ := newService(t, newClock(time.Now()))
		s.configure(t, func(cfg *Config, _ *Dependencies) { cfg.SubTitle = "From env" })

		got, err := s.Settings.SubscriptionSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if got.Title != "From env" || got.UpdateIntervalHours != panel.DefaultSubscriptionUpdateIntervalHours {
			t.Fatalf("settings = %+v", got)
		}

		stored := panel.SubscriptionSettings{Title: "From panel", UpdateIntervalHours: 6}
		if err := s.Settings.UpdateSubscriptionSettings(ctx, stored); err != nil {
			t.Fatal(err)
		}

		got, err = s.Settings.SubscriptionSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if got != stored {
			t.Fatalf("settings = %+v, want %+v", got, stored)
		}
	})

	t.Run("a field missing from an older document keeps its default", func(t *testing.T) {
		s, _ := newService(t, newClock(time.Now()))
		if err := s.Store.Settings.PutSetting(ctx, "subscription", `{"title":"Old"}`, time.Now()); err != nil {
			t.Fatal(err)
		}

		got, err := s.Settings.SubscriptionSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if got.Title != "Old" || got.UpdateIntervalHours != panel.DefaultSubscriptionUpdateIntervalHours {
			t.Fatalf("settings = %+v", got)
		}
	})

	t.Run("an invalid update changes nothing", func(t *testing.T) {
		s, _ := newService(t, newClock(time.Now()))

		err := s.Settings.UpdateSubscriptionSettings(ctx, panel.SubscriptionSettings{Title: "x", UpdateIntervalHours: 0})
		if !errors.Is(err, panel.ErrInvalid) {
			t.Fatalf("update: %v, want ErrInvalid", err)
		}

		if _, err := s.Store.Settings.GetSetting(ctx, "subscription"); !errors.Is(err, panel.ErrNotFound) {
			t.Fatalf("stored after a rejected update: %v", err)
		}
	})

	t.Run("a title is stored without surrounding spaces", func(t *testing.T) {
		s, _ := newService(t, newClock(time.Now()))

		in := panel.SubscriptionSettings{Title: "  VPN  ", UpdateIntervalHours: 12}
		if err := s.Settings.UpdateSubscriptionSettings(ctx, in); err != nil {
			t.Fatal(err)
		}

		got, err := s.Settings.SubscriptionSettings(ctx)
		if err != nil {
			t.Fatal(err)
		}

		if got.Title != "VPN" {
			t.Fatalf("title = %q", got.Title)
		}
	})

	for _, tt := range []struct{ name, doc string }{
		{"malformed document", `{"title":`},
		{"document that fails validation", `{"title":"x","update_interval_hours":0}`},
	} {
		t.Run(tt.name+" is an error, not the defaults", func(t *testing.T) {
			s, _ := newService(t, newClock(time.Now()))
			if err := s.Store.Settings.PutSetting(ctx, "subscription", tt.doc, time.Now()); err != nil {
				t.Fatal(err)
			}

			if got, err := s.Settings.SubscriptionSettings(ctx); err == nil {
				t.Fatalf("settings = %+v, want an error", got)
			}
		})
	}
}

// subscriptionServer serves the fixture's subscriptions the way the panel wires them.
func subscriptionServer(t *testing.T, s *serviceFixture) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	(&subapi.Handler{Source: subscription.Source{
		Users: s.Store.Users, Nodes: s.Store.Nodes, Hosts: s.Store.Hosts, Settings: s.Settings,
	}}).Register(mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv
}

func subscriptionToken(t *testing.T, s *serviceFixture, userID int64) string {
	t.Helper()

	u, err := s.Store.Users.GetUser(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}

	return u.Token
}

func fetch(t *testing.T, target string) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}

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

func TestSubscriptionUsesStoredSettingsInEveryFormat(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	s.configure(t, func(cfg *Config, _ *Dependencies) { cfg.SubTitle = "From env" })

	if _, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{
		Name: "n1", Host: "n1.example.com", VLESS: &panel.VLESSInput{},
	}); err != nil {
		t.Fatal(err)
	}

	u, err := s.Users.CreateUser(ctx, panel.CreateUserInput{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	token := subscriptionToken(t, s, u.ID)
	srv := subscriptionServer(t, s)
	formats := map[string]string{"links": "txt", "singbox": "json", "clash": "yaml"}

	before := map[string]string{}

	for format, ext := range formats {
		resp, body := fetch(t, srv.URL+"/sub/"+token+"?format="+format)
		assertSubscriptionHeaders(t, resp, "From env", panel.DefaultSubscriptionUpdateIntervalHours, ext)
		before[format] = body
	}

	if err := s.Settings.UpdateSubscriptionSettings(ctx, panel.SubscriptionSettings{
		Title: "Мой VPN", UpdateIntervalHours: 6,
	}); err != nil {
		t.Fatal(err)
	}

	for format, ext := range formats {
		resp, body := fetch(t, srv.URL+"/sub/"+token+"?format="+format)
		assertSubscriptionHeaders(t, resp, "Мой VPN", 6, ext)

		if body != before[format] {
			t.Errorf("%s: the settings changed the body", format)
		}
	}
}

func assertSubscriptionHeaders(t *testing.T, resp *http.Response, title string, interval int, ext string) {
	t.Helper()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	want := "base64:" + base64.StdEncoding.EncodeToString([]byte(title))
	if got := resp.Header.Get("Profile-Title"); got != want {
		t.Errorf("Profile-Title = %q, want %q", got, want)
	}

	if got := resp.Header.Get("Profile-Update-Interval"); got != strconv.Itoa(interval) {
		t.Errorf("Profile-Update-Interval = %q, want %d", got, interval)
	}

	if got := resp.Header.Get("Content-Disposition"); !strings.Contains(got, url.PathEscape(title)+"."+ext) {
		t.Errorf("Content-Disposition = %q, want the title and .%s", got, ext)
	}
}

func TestSubscriptionFailsOnBrokenSettings(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))

	u, err := s.Users.CreateUser(ctx, panel.CreateUserInput{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Store.Settings.PutSetting(ctx, "subscription", `{"title":`, time.Now()); err != nil {
		t.Fatal(err)
	}

	srv := subscriptionServer(t, s)

	if resp, _ := fetch(t, srv.URL+"/sub/"+subscriptionToken(t, s, u.ID)); resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("broken settings: status = %d", resp.StatusCode)
	}

	if resp, _ := fetch(t, srv.URL+"/sub/unknown"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown token: status = %d", resp.StatusCode)
	}
}

// adminClient signs in to a web interface mounted under base and returns a client that keeps the session.
func adminClient(t *testing.T, srv *httptest.Server, base string) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	resp, _ := postForm(t, c, srv, base+"/login", url.Values{"password": {"secret"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("login: status = %d", resp.StatusCode)
	}

	return c
}

func postForm(t *testing.T, c *http.Client, srv *httptest.Server, path string, form url.Values) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)

	return do(t, c, req)
}

func getPage(t *testing.T, c *http.Client, target string) (*http.Response, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}

	return do(t, c, req)
}

func do(t *testing.T, c *http.Client, req *http.Request) (*http.Response, string) {
	t.Helper()

	resp, err := c.Do(req)
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

func TestSettingsPage(t *testing.T) {
	for _, base := range []string{"", "/a1b2c3"} {
		t.Run("base "+base, func(t *testing.T) {
			ctx := context.Background()
			s, _ := newService(t, newClock(time.Now()))

			ui, err := web.New(web.Config{
				Dependencies: web.Dependencies{Settings: s.Settings},
				Password:     "secret",
				Base:         base,
			})
			if err != nil {
				t.Fatal(err)
			}

			mux := http.NewServeMux()
			ui.Register(mux)

			var handler http.Handler = mux
			if base != "" {
				handler = http.StripPrefix(base, mux)
			}

			srv := httptest.NewServer(handler)
			t.Cleanup(srv.Close)

			noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

			resp, _ := getPage(t, noRedirect, srv.URL+base+"/settings")
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("anonymous GET: status = %d", resp.StatusCode)
			}

			resp, _ = postForm(t, noRedirect, srv, base+"/settings", url.Values{"title": {"x"}, "update_interval_hours": {"6"}})
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("anonymous POST: status = %d", resp.StatusCode)
			}

			c := adminClient(t, srv, base)

			resp, body := getPage(t, c, srv.URL+base+"/settings")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET: status = %d", resp.StatusCode)
			}

			for _, want := range []string{
				`action="` + base + `/settings"`,
				`value="` + panel.DefaultSubscriptionTitle + `"`,
				`value="` + strconv.Itoa(panel.DefaultSubscriptionUpdateIntervalHours) + `"`,
				`href="` + base + `/settings"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("page lacks %q", want)
				}
			}

			if strings.Contains(body, "style=") || strings.Contains(body, "onclick=") {
				t.Error("the page breaks the CSP with inline styles or handlers")
			}

			resp, _ = postForm(t, c, srv, base+"/settings", url.Values{"title": {"Мой VPN"}, "update_interval_hours": {"6"}})
			if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), base+"/settings") {
				t.Fatalf("POST: status = %d, location = %q", resp.StatusCode, resp.Header.Get("Location"))
			}

			crossOrigin, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+base+"/settings",
				strings.NewReader(url.Values{"title": {"Forged"}, "update_interval_hours": {"1"}}.Encode()))
			if err != nil {
				t.Fatal(err)
			}

			crossOrigin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			crossOrigin.Header.Set("Origin", "http://evil.example")

			if resp, _ := do(t, c, crossOrigin); resp.StatusCode != http.StatusForbidden {
				t.Fatalf("cross-origin POST: status = %d", resp.StatusCode)
			}

			got, err := s.Settings.SubscriptionSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}

			if want := (panel.SubscriptionSettings{Title: "Мой VPN", UpdateIntervalHours: 6}); got != want {
				t.Fatalf("stored %+v, want %+v", got, want)
			}

			for _, tt := range []struct {
				name string
				form url.Values
				want string
			}{
				{"interval is not a number", url.Values{"title": {"x"}, "update_interval_hours": {"soon"}}, "from 1 to 168"},
				{"interval out of range", url.Values{"title": {"x"}, "update_interval_hours": {"0"}}, "from 1 to 168"},
				{"empty title", url.Values{"title": {""}, "update_interval_hours": {"6"}}, "title is required"},
			} {
				resp, body := postForm(t, c, srv, base+"/settings", tt.form)
				if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, tt.want) {
					t.Errorf("%s: status = %d, body = %q", tt.name, resp.StatusCode, body)
				}
			}

			got, err = s.Settings.SubscriptionSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}

			if got.Title != "Мой VPN" {
				t.Fatalf("a rejected form changed the settings: %+v", got)
			}
		})
	}
}

// settingsServer serves the settings page and signs in.
func settingsServer(t *testing.T, s *serviceFixture) (*httptest.Server, *http.Client) {
	t.Helper()

	ui, err := web.New(web.Config{Dependencies: web.Dependencies{Settings: s.Settings}, Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	ui.Register(mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, adminClient(t, srv, "")
}

func TestClashRoutingFromSettingsReachesSubscription(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))

	if _, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{
		Name: "n1", Host: "n1.example.com", VLESS: &panel.VLESSInput{},
	}); err != nil {
		t.Fatal(err)
	}

	u, err := s.Users.CreateUser(ctx, panel.CreateUserInput{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	sub := subscriptionServer(t, s).URL + "/sub/" + subscriptionToken(t, s, u.ID)
	admin, c := settingsServer(t, s)

	before := map[string]string{}
	for _, format := range []string{"links", "singbox"} {
		_, before[format] = fetch(t, sub+"?format="+format)
	}

	const rules = "rules:\n  - DOMAIN-SUFFIX,ir,DIRECT\n  - MATCH,proxy\n"

	resp, body := postForm(t, c, admin, "/settings", url.Values{
		"title": {"VPN"}, "update_interval_hours": {"12"}, "clash_routing_enabled": {"on"}, "clash_routing_yaml": {rules},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST: status = %d, body = %q", resp.StatusCode, body)
	}

	_, clash := fetch(t, sub+"?format=clash")
	if !strings.Contains(clash, "DOMAIN-SUFFIX,ir,DIRECT") || strings.Contains(clash, "ru-blocked") {
		t.Errorf("clash profile did not take the saved rules:\n%s", clash)
	}

	for format, want := range before {
		if _, got := fetch(t, sub+"?format="+format); got != want {
			t.Errorf("%s: Clash routing changed the body", format)
		}
	}

	_, page := getPage(t, c, admin.URL+"/settings")
	if !strings.Contains(page, "DOMAIN-SUFFIX,ir,DIRECT") || !strings.Contains(page, `name="clash_routing_enabled" checked`) {
		t.Errorf("the page does not show the saved rules:\n%s", page)
	}

	if strings.Contains(page, "style=") || strings.Contains(page, "onclick=") {
		t.Error("the page breaks the CSP with inline styles or handlers")
	}
}

// A rejected form keeps what was typed: the page is boosted, so htmx shows the error without leaving it.
func TestSettingsFormReportsInvalidRoutingThroughHTMX(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	admin, c := settingsServer(t, s)

	_, page := getPage(t, c, admin.URL+"/settings")
	if !strings.Contains(page, `hx-boost="true"`) {
		t.Error("the settings form is not boosted")
	}

	form := url.Values{"title": {"VPN"}, "update_interval_hours": {"12"}, "clash_routing_enabled": {"on"}, "clash_routing_yaml": {"proxies: []\n"}}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, admin.URL+"/settings", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", admin.URL)
	req.Header.Set("Hx-Request", "true")

	resp, _ := do(t, c, req)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(resp.Header.Get("Hx-Trigger"), "is not allowed") {
		t.Fatalf("status = %d, Hx-Trigger = %q", resp.StatusCode, resp.Header.Get("Hx-Trigger"))
	}

	got, err := s.Settings.SubscriptionSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if got.ClashRoutingEnabled || got.ClashRoutingYAML != "" {
		t.Fatalf("a rejected form changed the settings: %+v", got)
	}
}

func TestSubscriptionHeadersFromSettings(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))

	if _, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{
		Name: "n1", Host: "n1.example.com", VLESS: &panel.VLESSInput{},
	}); err != nil {
		t.Fatal(err)
	}

	u, err := s.Users.CreateUser(ctx, panel.CreateUserInput{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}

	sub := subscriptionServer(t, s).URL + "/sub/" + subscriptionToken(t, s, u.ID)
	admin, c := settingsServer(t, s)
	formats := []string{"links", "singbox", "clash"}
	headers := []string{"Support-Url", "Profile-Web-Page-Url", "Announce"}

	before := map[string]string{}

	for _, format := range formats {
		resp, body := fetch(t, sub+"?format="+format)
		before[format] = body

		for _, name := range headers {
			if _, set := resp.Header[name]; set {
				t.Errorf("%s: %s is sent without a setting", format, name)
			}
		}
	}

	const announce = "Плановые работы в субботу"

	resp, body := postForm(t, c, admin, "/settings", url.Values{
		"title": {"VPN"}, "update_interval_hours": {"12"},
		"support_url": {"https://t.me/support"}, "profile_page_url": {"https://example.com/vpn"}, "announce": {announce},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST: status = %d, body = %q", resp.StatusCode, body)
	}

	want := map[string]string{
		"Support-Url":          "https://t.me/support",
		"Profile-Web-Page-Url": "https://example.com/vpn",
		"Announce":             "base64:" + base64.StdEncoding.EncodeToString([]byte(announce)),
	}

	for _, format := range formats {
		resp, body := fetch(t, sub+"?format="+format)
		for name, value := range want {
			if got := resp.Header.Get(name); got != value {
				t.Errorf("%s: %s = %q, want %q", format, name, got, value)
			}
		}

		if body != before[format] {
			t.Errorf("%s: the headers changed the body", format)
		}
	}

	_, page := getPage(t, c, admin.URL+"/settings")
	for _, value := range []string{"https://t.me/support", "https://example.com/vpn", announce} {
		if !strings.Contains(page, value) {
			t.Errorf("the page does not show %q", value)
		}
	}

	resp, body = postForm(t, c, admin, "/settings", url.Values{
		"title": {"VPN"}, "update_interval_hours": {"12"}, "support_url": {"http://example.com"},
	})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "support link") {
		t.Errorf("plain HTTP link: status = %d, body = %q", resp.StatusCode, body)
	}

	if resp, _ := fetch(t, sub); resp.Header.Get("Support-Url") != want["Support-Url"] {
		t.Errorf("a rejected form changed the header: %q", resp.Header.Get("Support-Url"))
	}

	// Clearing a field removes its header.
	resp, _ = postForm(t, c, admin, "/settings", url.Values{"title": {"VPN"}, "update_interval_hours": {"12"}})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("clearing POST: status = %d", resp.StatusCode)
	}

	cleared, _ := fetch(t, sub)
	for _, name := range headers {
		if _, set := cleared.Header[name]; set {
			t.Errorf("%s is still sent after clearing it", name)
		}
	}
}
