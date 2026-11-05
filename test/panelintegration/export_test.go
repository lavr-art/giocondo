package panelintegration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

func TestWireGuardExport(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	n, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{Name: "wg-node", Host: "vpn.example.com", WireGuard: &panel.WGNodeInput{}})
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.Users.CreateUser(ctx, panel.CreateUserInput{Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	data, name, err := s.Queries.ExportWireGuard(ctx, u.ID, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	if name != "alice-wg-node.conf" || !strings.Contains(string(data), "Endpoint = vpn.example.com:51820") || !strings.Contains(string(data), "[Interface]") {
		t.Fatalf("export %q: %s", name, data)
	}
	if _, _, err = s.Queries.ExportWireGuard(ctx, -1, n.ID); !errors.Is(err, panel.ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	plain, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{Name: "plain", Host: "vpn.example.com", VLESS: &panel.VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Queries.ExportWireGuard(ctx, u.ID, plain.ID); !errors.Is(err, panel.ErrInvalid) {
		t.Fatalf("missing WG protocol: %v", err)
	}
}
