package panelintegration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	. "github.com/artemlavr/giocondo/test/paneltest" //nolint:revive // dot import keeps the test package call sites concise.
)

// newHostsNode creates a node with VLESS Reality (server_name www.example.com), Hysteria2 and
// WireGuard, with managed DNS when managed is set.
func newHostsNode(t *testing.T, managed bool) (*serviceFixture, int64) {
	t.Helper()

	ctx := context.Background()
	s, _ := newService(t, newClock(time.Now()))
	s.configure(t, func(cfg *Config, deps *Dependencies) {
		deps.DNS = &memoryDNS{}
		cfg.DNSDomain = "vpn.example.com"
	})

	n, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{
		Name: "n1", Host: "192.0.2.1", IP: "192.0.2.1",
		VLESS:     &panel.VLESSInput{Dest: "www.example.com:443", ServerName: "www.example.com"},
		Hysteria2: &panel.Hy2Input{},
		WireGuard: &panel.WGNodeInput{},
	})
	if err != nil {
		t.Fatal(err)
	}

	if managed {
		if _, err := s.Network.EnableDNS(ctx, n.ID, true); err != nil {
			t.Fatal(err)
		}
	}

	return s, n.ID
}

func TestHostOverrideValidation(t *testing.T) {
	tests := []struct {
		name    string
		managed bool
		in      panel.HostInput
		want    *panel.HostView // nil: rejected as invalid
	}{
		{
			name:    "managed node rejects an IP with surrounding spaces",
			managed: true,
			in:      panel.HostInput{Inbound: "vless", Address: " 192.0.2.5"},
		},
		{
			name:    "managed node rejects an IP",
			managed: true,
			in:      panel.HostInput{Inbound: "vless", Address: "192.0.2.5"},
		},
		{
			name:    "managed node accepts a domain",
			managed: true,
			in:      panel.HostInput{Inbound: "vless", Address: "cdn.example.com", Sort: 2},
			want:    &panel.HostView{InboundTag: "vless", Type: "vless-reality", Address: "cdn.example.com", Sort: 2},
		},
		{
			name: "unmanaged node accepts an IP",
			in:   panel.HostInput{Inbound: "vless", Address: "192.0.2.5"},
			want: &panel.HostView{InboundTag: "vless", Type: "vless-reality", Address: "192.0.2.5"},
		},
		{
			name: "Reality rejects a foreign SNI",
			in:   panel.HostInput{Inbound: "vless", Address: "cdn.example.com", SNI: "other.example.com"},
		},
		{
			name: "Reality accepts its own server_name",
			in:   panel.HostInput{Inbound: "vless", Address: "cdn.example.com", SNI: "www.example.com"},
			want: &panel.HostView{InboundTag: "vless", Type: "vless-reality", Address: "cdn.example.com", SNI: "www.example.com"},
		},
		{
			name: "Hysteria2 accepts any SNI and trims input",
			in:   panel.HostInput{Inbound: " hy2 ", Address: " cdn.example.com ", Port: 9443, SNI: " sni.example.com ", Path: " /p "},
			want: &panel.HostView{InboundTag: "hy2", Type: "hysteria2", Address: "cdn.example.com", Port: 9443, SNI: "sni.example.com", Path: "/p"},
		},
		{
			name: "WireGuard rejects an SNI",
			in:   panel.HostInput{Inbound: "wg", Address: "cdn.example.com", SNI: "sni.example.com"},
		},
		{
			name: "unknown inbound",
			in:   panel.HostInput{Inbound: "trojan", Address: "cdn.example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s, nodeID := newHostsNode(t, tt.managed)

			got, err := s.Hosts.CreateHost(ctx, nodeID, tt.in)

			stored, listErr := s.Hosts.ListHosts(ctx, nodeID)
			if listErr != nil {
				t.Fatal(listErr)
			}

			if tt.want == nil {
				if !errors.Is(err, panel.ErrInvalid) {
					t.Fatalf("CreateHost error = %v, want %v", err, panel.ErrInvalid)
				}

				if len(stored) != 0 {
					t.Fatalf("rejected override stored: %+v", stored)
				}

				return
			}

			if err != nil {
				t.Fatalf("CreateHost: %v", err)
			}

			want := *tt.want
			want.ID = got.ID

			if got != want || len(stored) != 1 || stored[0] != want {
				t.Fatalf("created %+v, stored %+v; want %+v", got, stored, want)
			}
		})
	}
}

func TestHostOverrideUpdateKeepsManagedDNS(t *testing.T) {
	ctx := context.Background()
	s, nodeID := newHostsNode(t, true)

	created, err := s.Hosts.CreateHost(ctx, nodeID, panel.HostInput{Inbound: "vless", Address: "cdn.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	for _, address := range []string{"192.0.2.5", " 192.0.2.5 "} {
		if _, err := s.Hosts.UpdateHost(ctx, created.ID, panel.HostInput{Address: address}); !errors.Is(err, panel.ErrInvalid) {
			t.Fatalf("UpdateHost(%q) error = %v, want %v", address, err, panel.ErrInvalid)
		}
	}

	stored, err := s.Hosts.ListHosts(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}

	if len(stored) != 1 || stored[0] != created {
		t.Fatalf("stored %+v, want unchanged %+v", stored, created)
	}
}
