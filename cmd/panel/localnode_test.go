package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/pkifs"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
	"github.com/artemlavr/giocondo/internal/repository/sqlite/nodes"
	"github.com/artemlavr/giocondo/internal/service/pki"
)

type fakeRuntime struct {
	node panel.Node
	cert panel.AgentCert
	n    int
}

func (f *fakeRuntime) Install(_ context.Context, n panel.Node, c panel.AgentCert) error {
	f.node, f.cert = n, c
	f.n++
	return nil
}

func TestLocalNode(t *testing.T) {
	dir := t.TempDir()
	db, pkiDir := filepath.Join(dir, "panel.db"), filepath.Join(dir, "pki")

	if err := (pki.Certificates{Store: pkifs.Disk{Dir: pkiDir}}).InitCA(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := (pki.Certificates{Store: pkifs.Disk{Dir: pkiDir}}).IssueServer(context.Background(), []string{"panel.example.com"}); err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{}
	var out bytes.Buffer
	base := []string{"-db", db, "-pki", pkiDir, "-grpc", ":9443"}

	run := func(extra ...string) error {
		return runLocalNode(context.Background(), append(append([]string{}, base...), extra...), &out, rt)
	}

	if err := run("-host", "vpn.example.com"); err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("certificate without 127.0.0.1: %v", err)
	}
	if err := (pki.Certificates{Store: pkifs.Disk{Dir: pkiDir}}).IssueServer(context.Background(), []string{"panel.example.com", "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}

	if err := run(); err == nil || !strings.Contains(err.Error(), "-host") {
		t.Fatalf("new node without host: %v", err)
	}
	if err := run("-host", "vpn.example.com", "-protocols", "hysteria2"); err != nil {
		t.Fatal(err)
	}
	if rt.n != 1 || rt.node.Runtime != panel.RuntimeLocal || rt.node.Host != "vpn.example.com" ||
		rt.cert.PanelAddr != "127.0.0.1:9443" || !strings.Contains(rt.cert.AgentKey, "PRIVATE KEY") {
		t.Fatalf("installed %+v %+v", rt.node, rt.cert.PanelAddr)
	}

	// Second run: the node already exists, the certificate is reissued.
	if err := run(); err != nil || rt.n != 2 {
		t.Fatalf("rerun: %v (installs %d)", err, rt.n)
	}

	st, err := sqlite.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}

	if err := nodes.New(st).CreateNode(context.Background(), &panel.Node{Name: "remote1", Host: "r.example.com"}); err != nil {
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

func TestLocalNodeReadOnlyModesMissingDatabase(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "missing.db")

	for _, mode := range []string{"-check", "-ports"} {
		rt := &fakeRuntime{}
		args := []string{mode, "-db", db, "-agent-cert", filepath.Join(dir, "missing.crt"), "-timeout", "100ms"}

		if err := runLocalNode(context.Background(), args, &bytes.Buffer{}, rt); err == nil {
			t.Errorf("%s: missing database accepted", mode)
		}
		if rt.n != 0 {
			t.Errorf("%s: read-only mode installed credentials", mode)
		}
		if _, err := os.Stat(db); !os.IsNotExist(err) {
			t.Fatalf("%s: database created", mode)
		}
	}
}
