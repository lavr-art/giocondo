package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artemlavr/giocondo/internal/admin"
	"github.com/artemlavr/giocondo/internal/nodeapi/pki"
	"github.com/artemlavr/giocondo/internal/store"
)

type fakeRuntime struct {
	node store.Node
	cert admin.AgentCert
	n    int
}

func (f *fakeRuntime) Install(_ context.Context, n store.Node, c admin.AgentCert) error {
	f.node, f.cert = n, c
	f.n++
	return nil
}

func TestLocalNode(t *testing.T) {
	dir := t.TempDir()
	db, pkiDir := filepath.Join(dir, "panel.db"), filepath.Join(dir, "pki")
	if err := pki.InitCA(pkiDir); err != nil {
		t.Fatal(err)
	}
	if err := pki.IssueServer(pkiDir, []string{"panel.example.com"}); err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{}
	var out bytes.Buffer
	base := []string{"-db", db, "-pki", pkiDir, "-grpc", ":9443"}
	run := func(extra ...string) error {
		return runLocalNode(append(append([]string{}, base...), extra...), &out, rt)
	}

	if err := run("-host", "vpn.example.com"); err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("certificate without 127.0.0.1: %v", err)
	}
	if err := pki.IssueServer(pkiDir, []string{"panel.example.com", "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil || !strings.Contains(err.Error(), "-host") {
		t.Fatalf("new node without host: %v", err)
	}
	if err := run("-host", "vpn.example.com", "-protocols", "hysteria2"); err != nil {
		t.Fatal(err)
	}
	if rt.n != 1 || rt.node.Runtime != store.RuntimeLocal || rt.node.Host != "vpn.example.com" ||
		rt.cert.PanelAddr != "127.0.0.1:9443" || !strings.Contains(rt.cert.AgentKey, "PRIVATE KEY") {
		t.Fatalf("installed %+v %+v", rt.node, rt.cert.PanelAddr)
	}
	// Second run: the node already exists, the certificate is reissued.
	if err := run(); err != nil || rt.n != 2 {
		t.Fatalf("rerun: %v (installs %d)", err, rt.n)
	}

	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateNode(context.Background(), &store.Node{Name: "remote1", Host: "r.example.com"}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if err := run("-name", "remote1"); err == nil || !strings.Contains(err.Error(), "not local") {
		t.Errorf("remote node: %v", err)
	}
	if err := run("-grpc", "off"); err == nil {
		t.Error("grpc off accepted")
	}
}
