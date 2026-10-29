package singbox

import (
	"strings"
	"testing"
)

func TestValidateOK(t *testing.T) {
	for _, name := range []string{"vless", "hysteria2-selfsigned", "hysteria2-acme", "wireguard", "all"} {
		f := loadTestFile(t, name)
		if err := Validate(&f.Node, f.Users); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(f *testFile)
		want   string
	}{
		{"no host", func(f *testFile) { f.Node.Host = "" }, "host is required"},
		{"no inbounds", func(f *testFile) { f.Node.Inbounds = nil }, "at least one inbound"},
		{"dup tag", func(f *testFile) { f.Node.Inbounds[1].Tag = f.Node.Inbounds[0].Tag }, "duplicate tag"},
		{"dup udp port", func(f *testFile) { f.Node.Inbounds[2].Port = f.Node.Inbounds[1].Port }, "already used"},
		{"zero port", func(f *testFile) { f.Node.Inbounds[0].Port = 0 }, "port is required"},
		{"unknown type", func(f *testFile) { f.Node.Inbounds[0].Type = "socks" }, "unknown type"},
		{"no reality key", func(f *testFile) { f.Node.Inbounds[0].Reality.PrivateKey = "" }, "private_key is empty"},
		{"bad short id", func(f *testFile) { f.Node.Inbounds[0].Reality.ShortIDs = []string{"xyz"} }, "short_id"},
		{"no sni", func(f *testFile) { f.Node.Inbounds[0].Reality.ServerName = "" }, "server_name is required"},
		{"no cert", func(f *testFile) { f.Node.Inbounds[1].Hysteria2.CertPEM = "" }, "cert_pem and key_pem"},
		{"acme on ip", func(f *testFile) {
			f.Node.Host = "203.0.113.10"
			f.Node.Inbounds[1].Hysteria2.TLS = TLSACME
		}, "acme needs a domain"},
		{"bad tls mode", func(f *testFile) { f.Node.Inbounds[1].Hysteria2.TLS = "plain" }, "unknown tls mode"},
		{"two wg", func(f *testFile) {
			wg := f.Node.Inbounds[2]
			wg.Tag, wg.Port = "wg2", 51821
			f.Node.Inbounds = append(f.Node.Inbounds, wg)
		}, "only one wireguard"},
		{"bad name", func(f *testFile) { f.Users[0].Name = "bad name" }, "name must match"},
		{"dup name", func(f *testFile) { f.Users[1].Name = f.Users[0].Name }, "duplicate name"},
		{"bad uuid", func(f *testFile) { f.Users[0].UUID = "123" }, "invalid uuid"},
		{"dup uuid", func(f *testFile) { f.Users[1].UUID = strings.ToUpper(f.Users[0].UUID) }, "duplicate uuid"},
		{"no wg peer", func(f *testFile) { f.Users[0].WG = nil }, "wg peer is required"},
		{"wg key mismatch", func(f *testFile) { f.Users[0].WG.PublicKey = f.Users[1].WG.PublicKey }, "does not match"},
		{"wg addr outside", func(f *testFile) { f.Users[0].WG.Address = "10.71.0.2/32" }, "outside"},
		{"wg addr server", func(f *testFile) { f.Users[0].WG.Address = "10.70.0.1/32" }, "reserved for the server"},
		{"wg addr not /32", func(f *testFile) { f.Users[0].WG.Address = "10.70.0.2/24" }, "must be a /32"},
		{"wg dup addr", func(f *testFile) { f.Users[1].WG.Address = f.Users[0].WG.Address }, "duplicate wg address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := loadTestFile(t, "all")
			tt.mutate(&f)
			err := Validate(&f.Node, f.Users)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if _, err := Build(&f.Node, f.Users, Options{}); err == nil {
				t.Fatal("Build must fail on invalid input")
			}
		})
	}
}
