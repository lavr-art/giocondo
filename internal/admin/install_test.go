package admin

import (
	"testing"

	"github.com/artemlavr/giocondo/internal/singbox"
	"github.com/artemlavr/giocondo/internal/store"
)

func TestFirewallSpec(t *testing.T) {
	ins := []store.Inbound{
		{Spec: singbox.Inbound{Type: singbox.TypeVLESSReality, Port: 443}},
		{Spec: singbox.Inbound{Type: singbox.TypeHysteria2, Port: 8443, Hysteria2: &singbox.Hysteria2Params{TLS: singbox.TLSACME}}},
		{Spec: singbox.Inbound{Type: singbox.TypeWireGuard, Port: 51820}},
		{Spec: singbox.Inbound{Type: singbox.TypeHysteria2, Port: 8443}}, // a repeat is not duplicated
	}
	if got, want := firewallSpec(ins), "443/tcp,8443/udp,80/tcp,51820/udp"; got != want {
		t.Errorf("firewallSpec = %q, want %q", got, want)
	}
	if got := firewallSpec(nil); got != "" {
		t.Errorf("no inbounds: %q", got)
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{"a:1": "'a:1'", "it's": `'it'\''s'`, "": "''"} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}
