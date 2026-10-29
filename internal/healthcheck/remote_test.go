package healthcheck

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/artemlavr/giocondo/internal/singbox"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteOnlySendsDedicatedCredentials(t *testing.T) {
	priv, _, _ := singbox.NewWGKeypair()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Inbound.WireGuard.PrivateKey != "" || req.WGPublicKey == "" {
			t.Error("server private key crossed probe boundary")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"error": ""})
	}))
	defer server.Close()
	remote := &Remote{URL: server.URL, Token: "test", Client: server.Client()}
	err := remote.Check(context.Background(), Request{IP: "192.0.2.1", Inbound: singbox.Inbound{Type: singbox.TypeWireGuard, WireGuard: &singbox.WireGuardParams{PrivateKey: priv}}})
	if err != nil {
		t.Fatal(err)
	}
}
func TestProbeAllowlistAndUnavailable(t *testing.T) {
	calls := 0
	c := CheckerFunc(func(context.Context, Request) error { calls++; return nil })
	h := Handler("token", map[string]bool{"192.0.2.1:443/vless-reality": true}, c)
	for _, tc := range []struct {
		auth, ip string
		want     int
	}{{"", "192.0.2.1", 401}, {"Bearer token", "127.0.0.1", 403}, {"Bearer token", "192.0.2.1", 200}} {
		req := httptest.NewRequest("POST", "/", strings.NewReader(`{"ip":"`+tc.ip+`","inbound":{"type":"vless-reality","port":443}}`))
		req.Header.Set("Authorization", tc.auth)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("status %d want %d", w.Code, tc.want)
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	remote := &Remote{URL: "http://127.0.0.1", Token: "token"}
	if !errors.Is(remote.Check(context.Background(), Request{}), ErrUnavailable) {
		t.Fatal("insecure remote must be unavailable")
	}
}
func TestMisconfiguredProbeIsNotNodeFailure(t *testing.T) {
	err := (&Local{}).Check(context.Background(), Request{Inbound: singbox.Inbound{Type: singbox.TypeHysteria2}})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want monitoring error, got %v", err)
	}
}
