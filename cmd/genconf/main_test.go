package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFillBuildInfo(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "node.yaml")
	src, err := os.ReadFile("../../deploy/examples/node.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(in, src, 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "config.json")
	var stdout, stderr bytes.Buffer

	// It cannot be built without secrets.
	if err := run([]string{"build", "-in", in, "-out", out}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "genconf fill") {
		t.Fatalf("build before fill: %v", err)
	}

	if err := run([]string{"fill", "-in", in}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	filled, _ := os.ReadFile(in)
	if err := run([]string{"fill", "-in", in}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(in); !bytes.Equal(filled, again) {
		t.Error("second fill changed the file")
	}
	if st, _ := os.Stat(in); st.Mode().Perm() != 0o600 {
		t.Errorf("filled yaml mode %v, want 0600", st.Mode().Perm())
	}

	if err := run([]string{"build", "-in", in, "-out", out}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config is not JSON: %v", err)
	}
	if len(cfg["inbounds"].([]any)) != 2 || len(cfg["endpoints"].([]any)) != 1 {
		t.Errorf("unexpected config shape: %s", data)
	}

	stdout.Reset()
	if err := run([]string{"info", "-in", in, "-user", "artem"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"public_key", "pinSHA256", "user artem", "[Interface]", "Endpoint = n1.example.com:51820"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("info has no %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "user test1") {
		t.Error("-user filter ignored")
	}
	if err := run([]string{"info", "-in", in, "-user", "nobody"}, &stdout, &stderr); err == nil {
		t.Error("expected error for unknown user")
	}
}

func TestUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	if err := run(nil, &out, &out); err == nil {
		t.Error("expected usage error")
	}
	if err := run([]string{"bogus"}, &out, &out); err == nil {
		t.Error("expected error")
	}
}
