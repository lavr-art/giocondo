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

func TestProvisionCommand(t *testing.T) {
	dir := t.TempDir()
	db, pkiDir := filepath.Join(dir, "panel.db"), filepath.Join(dir, "pki")
	if err := pki.InitCA(pkiDir); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	svc := &admin.Service{Store: st}
	if _, err := svc.CreateNode(context.Background(), admin.CreateNodeInput{Name: "n1", Host: "n1.example.com", Hysteria2: &admin.Hy2Input{}}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	t.Setenv("PANEL_AGENT_ADDR", "panel.example.com:9443")
	base := []string{"-db", db, "-pki", pkiDir, "-node", "n1"}

	var out bytes.Buffer
	if err := runProvision(append([]string{"cloud-init"}, base...), nil, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "#cloud-config") || !strings.Contains(out.String(), "panel.example.com:9443") {
		t.Errorf("cloud-init output:\n%s", out.String())
	}
	out.Reset()
	err = runProvision(append([]string{"ssh", "-host", "127.0.0.1", "-port", "1", "-password-stdin"}, base...), strings.NewReader("pw\n"), &out)
	if err == nil {
		t.Error("ssh to a closed port succeeded")
	}
	if err := runProvision([]string{"ssh", "-db", db}, nil, &out); err == nil || !strings.Contains(err.Error(), "-node") {
		t.Errorf("without -node: %v", err)
	}
	if err := runProvision(append([]string{"cloud-init"}, "-db", db, "-pki", pkiDir, "-node", "ghost"), nil, &out); err == nil {
		t.Error("unknown node accepted")
	}
}
