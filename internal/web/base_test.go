package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/artemlavr/giocondo/internal/admin"
	"github.com/artemlavr/giocondo/internal/store"
)

// The admin UI behind http.StripPrefix: links, redirects and cookies carry the prefix.
func TestBasePath(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h, err := New(Config{Admin: &admin.Service{Store: st}, Password: testPassword, Base: "/s3cret/"})
	if err != nil {
		t.Fatal(err)
	}
	inner := http.NewServeMux()
	h.Register(inner)
	root := http.NewServeMux()
	root.Handle("/s3cret/", http.StripPrefix("/s3cret", inner))
	srv := httptest.NewServer(root)
	t.Cleanup(srv.Close)
	e := &env{srv: srv, st: st, h: h}
	c := e.client(t)

	resp, _ := c.do("GET", "/s3cret/users", nil, nil)
	if loc := resp.Header.Get("Location"); resp.StatusCode != http.StatusSeeOther || loc != "/s3cret/login?next=%2Fusers" {
		t.Fatalf("unauthenticated: %d %s", resp.StatusCode, loc)
	}
	resp, _ = c.do("GET", "/s3cret/users", nil, map[string]string{"HX-Request": "true"})
	if resp.Header.Get("HX-Redirect") != "/s3cret/login" {
		t.Errorf("HX-Redirect %q", resp.Header.Get("HX-Redirect"))
	}
	resp, _ = c.do("POST", "/s3cret/login", url.Values{"password": {testPassword}, "next": {"/users"}}, nil)
	if resp.Header.Get("Location") != "/s3cret/users" {
		t.Fatalf("after login: %s", resp.Header.Get("Location"))
	}
	if ck := resp.Cookies(); len(ck) != 1 || ck[0].Path != "/s3cret" {
		t.Errorf("cookie %+v", ck)
	}
	_, body := c.do("GET", "/s3cret/users", nil, nil)
	// All links and forms on the page are inside the prefix.
	for _, m := range regexp.MustCompile(`(?:href|action|src|hx-post)="(/[^"]*)"`).FindAllStringSubmatch(body, -1) {
		if !strings.HasPrefix(m[1], "/s3cret/") {
			t.Errorf("link without prefix: %s", m[1])
		}
	}
	resp, _ = c.do("POST", "/s3cret/users", url.Values{"name": {"alice"}}, nil)
	if !strings.HasPrefix(resp.Header.Get("Location"), "/s3cret/users/") {
		t.Errorf("create user redirect %s", resp.Header.Get("Location"))
	}
}

func TestTokensPage(t *testing.T) {
	e := newEnv(t, testPassword)
	c := e.client(t)
	c.login()
	_, body := c.do("POST", "/tokens", url.Values{"name": {"grafana"}, "scope": {"monitor"}}, nil)
	secret := regexp.MustCompile(`value="(vpc_[^"]+)"`).FindStringSubmatch(body)
	if secret == nil {
		t.Fatalf("no token in response:\n%s", body)
	}
	if _, err := e.svc.Authenticate(context.Background(), secret[1]); err != nil {
		t.Errorf("shown token does not work: %v", err)
	}
	_, body = c.do("GET", "/tokens", nil, nil)
	if !strings.Contains(body, "grafana") || strings.Contains(body, secret[1]) {
		t.Error("token list must show the name but not the secret")
	}
	if resp, _ := c.do("POST", "/tokens", url.Values{"name": {"x"}, "scope": {"root"}}, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad scope: %d", resp.StatusCode)
	}
	list, _ := e.svc.ListTokens(context.Background())
	if resp, _ := c.do("POST", "/tokens/"+itoa(list[0].ID)+"/delete", nil, nil); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("delete: %d", resp.StatusCode)
	}
	if list, _ := e.svc.ListTokens(context.Background()); len(list) != 0 {
		t.Errorf("token not deleted: %v", list)
	}
}
