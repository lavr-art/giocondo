package panelintegration

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
	. "github.com/artemlavr/giocondo/test/paneltest" //nolint:revive // dot import keeps the test package call sites concise.
)

func TestNewAllowsDisabledFacilities(t *testing.T) {
	st, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := New(Config{}, Dependencies{DB: st})
	t.Cleanup(func() { _ = svc.Close() })
	if _, _, err = svc.Tokens.CreateToken(context.Background(), "token", "monitor"); err != nil {
		t.Fatal(err)
	}
	n, err := svc.Nodes.CreateNode(context.Background(), panel.CreateNodeInput{Name: "n", Host: "example.com", VLESS: &panel.VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.Provisioning.IssueAgentCert(context.Background(), n.ID); !errors.Is(err, panel.ErrInvalid) {
		t.Fatalf("disabled PKI: %v", err)
	}
	if err = svc.Health.CheckNodesOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}
