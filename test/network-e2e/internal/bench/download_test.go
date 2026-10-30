package bench

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBaselineSelectionVerifiesCachedReleaseAndScopesToken(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	assets := packageFixture(t, "1.2.2", "1.2.2", "1.2.2")
	root := t.TempDir()
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		var data []byte
		if r.URL.Host == "api.github.com" {
			if r.Header.Get("Authorization") != "Bearer test-token" {
				t.Error("GitHub API request missing token")
			}
			data = []byte(`[{"tag_name":"v9.0.0","draft":true},{"tag_name":"v2.0.0-rc1","prerelease":true},{"tag_name":"v1.2.2","draft":false,"prerelease":false}]`)
		} else {
			if r.URL.Host != "github.com" {
				return nil, fmt.Errorf("unexpected release host %s", r.URL.Host)
			}
			if r.Header.Get("Authorization") != "" {
				t.Error("API token leaked to download host")
			}
			if !strings.Contains(r.URL.Path, "/releases/download/v1.2.2/") {
				t.Errorf("release path = %s", r.URL.Path)
			}
			var err error
			data, err = os.ReadFile(filepath.Join(assets, filepath.Base(r.URL.Path)))
			if err != nil {
				return nil, err
			}
		}
		if r.Header.Get("User-Agent") != "giocondo-upgrade-tests" {
			t.Error("release request missing User-Agent")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(data))), Header: make(http.Header)}, nil
	})}
	if err := prepareBaseline(context.Background(), root, "arm64", "", client); err != nil {
		t.Fatal(err)
	}
	if requests != 5 {
		t.Fatalf("requests = %d; want API plus four release assets", requests)
	}
	version, err := os.ReadFile(filepath.Join(root, "baseline-version"))
	if err != nil || string(version) != "1.2.2" {
		t.Fatalf("baseline version = %q: %v", version, err)
	}
	if _, err := selectPackages(filepath.Join(root, "baseline"), "arm64"); err != nil {
		t.Fatal(err)
	}
	if err := prepareBaseline(context.Background(), root, "arm64", "v1.2.2", client); err != nil {
		t.Fatal(err)
	}
	if requests != 5 {
		t.Fatalf("cached baseline was downloaded again: %d requests", requests)
	}
	if err := os.WriteFile(filepath.Join(root, "downloads/v1.2.2/giocondo-vpn-agent_1.2.2_arm64.deb"), []byte("corrupted cached artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	err = prepareBaseline(context.Background(), root, "arm64", "v1.2.2", client)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("corrupt cached release error = %v", err)
	}
	if requests != 5 {
		t.Fatal("corrupt cached packages caused unexpected download")
	}
}

func TestBaselineRejectsUnstableVersionAndReleaseHTTPErrors(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable")), Header: make(http.Header)}, nil
	})}
	for _, tag := range []string{"1.2.3", "v1.2.3-rc1", "../../escape"} {
		err := prepareBaseline(context.Background(), t.TempDir(), "arm64", tag, client)
		if err == nil || !strings.Contains(err.Error(), "stable vX.Y.Z") {
			t.Fatalf("tag %q error = %v", tag, err)
		}
	}
	err := prepareBaseline(context.Background(), t.TempDir(), "arm64", "", client)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("API status error = %v", err)
	}
	err = prepareBaseline(context.Background(), t.TempDir(), "arm64", "v1.2.2", client)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("asset status error = %v", err)
	}
}
