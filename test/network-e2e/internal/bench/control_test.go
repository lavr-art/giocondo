package bench

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func TestControlAPIAuthenticatesAndPersistsRecords(t *testing.T) {
	root := t.TempDir()
	fixture, err := NewControl(root, "http://panel:8081")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(fixture)
	defer server.Close()
	request := func(method, path, body string, authorized bool) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if authorized {
			req.Header.Set("Authorization", "Bearer local-test-token")
		}
		r, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Body.Close() }()
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		return r.StatusCode, data
	}
	if status, _ := request("GET", "/zones/test/dns_records", "", false); status != 403 {
		t.Fatalf("unauthorized status = %d", status)
	}
	status, data := request("POST", "/zones/test/dns_records", `{"name":"node.test","type":"A","content":"192.0.2.8","ttl":60}`, true)
	if status != 200 || !bytes.Contains(data, []byte(`"id":"record-1"`)) {
		t.Fatalf("create: %d %s", status, data)
	}
	status, data = request("GET", "/zones/test/dns_records?name=other.test", "", true)
	if status != 200 || !bytes.Contains(data, []byte(`"result":[]`)) {
		t.Fatalf("filtered records: %d %s", status, data)
	}
	status, data = request("PATCH", "/zones/test/dns_records/record-1", `{"name":"node.test","type":"A","content":"192.0.2.9","ttl":120}`, true)
	if status != 200 || !bytes.Contains(data, []byte(`"content":"192.0.2.9"`)) {
		t.Fatalf("patch: %d %s", status, data)
	}
	if status, _ := request("PATCH", "/zones/test/dns_records/missing", `{}`, true); status != 404 {
		t.Fatalf("missing patch status = %d", status)
	}
	if status, _ := request("POST", "/zones/test/dns_records", `{`, true); status != 400 {
		t.Fatalf("malformed JSON status = %d", status)
	}
	reloaded, err := NewControl(root, "http://panel:8081")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/zones/test/dns_records?name=node.test", nil)
	req.Header.Set("Authorization", "Bearer local-test-token")
	w := httptest.NewRecorder()
	reloaded.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"content":"192.0.2.9"`) {
		t.Fatalf("persisted response: %d %s", w.Code, w.Body.String())
	}
}

func TestControlEventsStaticFilesAndChallenge(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/health/challenge?challenge=abc" {
			t.Errorf("upstream URI = %s", r.URL.RequestURI())
		}
		w.WriteHeader(202)
		_, _ = w.Write([]byte("abc"))
	}))
	defer upstream.Close()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "repo"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "repo", "Release"), []byte("signed repo"), 0600); err != nil {
		t.Fatal(err)
	}
	fixture, err := NewControl(root, upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
		contains           string
	}{
		{"GET", "/events", "", 200, "[]"},
		{"POST", "/events", `{"type":"node.online"}`, 200, "{}"},
		{"GET", "/events", "", 200, `"node.online"`},
		{"GET", "/repo/Release", "", 200, "signed repo"},
		{"GET", "/health/challenge?challenge=abc", "", 202, "abc"},
	} {
		w := httptest.NewRecorder()
		fixture.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if err := os.WriteFile(filepath.Join(root, "events.jsonl"), []byte("malformed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	fixture.ServeHTTP(w, httptest.NewRequest("GET", "/events", nil))
	if w.Code != 500 {
		t.Fatalf("corrupt event log status = %d", w.Code)
	}
}

func TestControlDNSRecordsTTLAndMalformedMessages(t *testing.T) {
	root := t.TempDir()
	records := `[{"id":"record-1","name":"node.test","type":"A","content":"192.0.2.9","ttl":120},{"id":"record-2","name":"node.test","type":"AAAA","content":"2001:db8::1","ttl":90}]`
	if err := os.WriteFile(filepath.Join(root, "records.json"), []byte(records), 0600); err != nil {
		t.Fatal(err)
	}
	fixture, err := NewControl(root, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		typ   dnsmessage.Type
		count int
		ttl   uint32
	}{{dnsmessage.TypeA, 1, 120}, {dnsmessage.TypeAAAA, 1, 90}, {dnsmessage.TypeTXT, 0, 0}} {
		query := dnsmessage.Message{Header: dnsmessage.Header{ID: 42, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("NODE.test."), Type: tc.typ, Class: dnsmessage.ClassINET}}}
		raw, err := query.Pack()
		if err != nil {
			t.Fatal(err)
		}
		out, err := fixture.AnswerDNS(raw)
		if err != nil {
			t.Fatal(err)
		}
		var response dnsmessage.Message
		if err := response.Unpack(out); err != nil {
			t.Fatal(err)
		}
		if response.ID != 42 || !response.Response || !response.Authoritative || len(response.Answers) != tc.count {
			t.Fatalf("DNS response = %+v", response)
		}
		if tc.count > 0 && response.Answers[0].Header.TTL != tc.ttl {
			t.Fatalf("TTL = %d", response.Answers[0].Header.TTL)
		}
	}
	for _, data := range [][]byte{nil, {1, 2, 3}, make([]byte, 12), {0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0xc0, 0x0c, 0, 1, 0, 1}} {
		if _, err := fixture.AnswerDNS(data); err == nil {
			t.Fatalf("malformed DNS accepted: %x", data)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "records.json"), []byte("[bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewControl(root, ""); err == nil {
		t.Fatal("corrupt persisted records accepted")
	}
}

func TestRequestChecksStatusAndPreservesAuthorization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if r.URL.Path == "/bad" {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"error":"forbidden"}`))
			return
		}
		if r.Method == "POST" {
			var data map[string]string
			if err := json.NewDecoder(r.Body).Decode(&data); err != nil || data["name"] != "test" {
				t.Errorf("body = %v: %v", data, err)
			}
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	var out bytes.Buffer
	if err := HTTPRequest(context.Background(), server.Client(), "POST", server.URL+"/ok", `{"name":"test"}`, "secret", &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "{}" {
		t.Fatalf("empty response = %q", out.String())
	}
	out.Reset()
	err := HTTPRequest(context.Background(), server.Client(), "GET", server.URL+"/bad", "", "secret", &out)
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("HTTP error = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("failed response written to stdout: %s", out.String())
	}
}
