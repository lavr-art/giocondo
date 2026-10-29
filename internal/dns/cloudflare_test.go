package dns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCloudflareEnsureAndConflict(t *testing.T) {
	var record Record
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing auth")
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "GET":
			if record.ID == "" {
				_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []Record{record}})
		case "POST", "PATCH":
			if err := json.NewDecoder(r.Body).Decode(&record); err != nil {
				t.Error(err)
			}
			record.ID = "rec1"
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": record})
		}
	}))
	defer srv.Close()
	c := &Cloudflare{Token: "secret", ZoneID: "zone", BaseURL: srv.URL, Client: srv.Client()}
	ctx := context.Background()
	got, err := c.Ensure(ctx, "n1.example.com", "192.0.2.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "rec1" || got.TTL != 60 || got.Proxied || got.Type != "A" {
		t.Fatalf("record %+v", got)
	}
	if _, err = c.Ensure(ctx, got.Name, "192.0.2.2", got.ID); err != nil {
		t.Fatal(err)
	}
	if record.Content != "192.0.2.2" {
		t.Fatal(record)
	}
	before := calls
	if _, err = c.Ensure(ctx, got.Name, "192.0.2.3", ""); err == nil {
		t.Fatal("must reject unowned record")
	}
	if calls != before+1 {
		t.Fatal("conflict must not write")
	}
}

func TestCloudflareFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"forbidden"}]}`))
	}))
	defer srv.Close()
	c := &Cloudflare{Token: "secret", ZoneID: "zone", BaseURL: srv.URL, Client: srv.Client()}
	if _, err := c.Ensure(context.Background(), "n1.example.com", "192.0.2.1", ""); err == nil {
		t.Fatal("expected API failure")
	}
}
