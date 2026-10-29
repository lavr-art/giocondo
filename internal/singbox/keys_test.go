package singbox

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestRealityKeypair(t *testing.T) {
	priv, pub, err := NewRealityKeypair()
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{priv, pub} {
		raw, err := base64.RawURLEncoding.DecodeString(k)
		if err != nil || len(raw) != 32 {
			t.Fatalf("key %q: len %d, err %v", k, len(raw), err)
		}
	}
	derived, err := RealityPublicKey(priv)
	if err != nil || derived != pub {
		t.Fatalf("RealityPublicKey = %q, %v; want %q", derived, err, pub)
	}
	if _, err := RealityPublicKey("not-a-key"); err == nil {
		t.Error("expected error for bad key")
	}
}

func TestWGKeypair(t *testing.T) {
	priv, pub, err := NewWGKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseWGKey(priv); err != nil {
		t.Fatal(err)
	}
	derived, err := WGPublicKey(priv)
	if err != nil || derived != pub {
		t.Fatalf("WGPublicKey = %q, %v; want %q", derived, err, pub)
	}
	psk, err := NewPresharedKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseWGKey(psk); err != nil {
		t.Fatal(err)
	}
	if _, err := parseWGKey(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Error("expected error for short key")
	}
}

func TestShortIDAndUUID(t *testing.T) {
	id, err := NewShortID()
	if err != nil || len(id) != 8 || !isShortID(id) {
		t.Fatalf("short id %q, %v", id, err)
	}
	u, err := NewUUID()
	if err != nil || !uuidRe.MatchString(u) || u[14] != '4' {
		t.Fatalf("uuid %q, %v", u, err)
	}
}

func TestSelfSignedCert(t *testing.T) {
	for _, host := range []string{"n1.example.com", "203.0.113.10"} {
		certPEM, keyPEM, err := SelfSignedCert(host)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := pem.Decode([]byte(certPEM))
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if err := cert.VerifyHostname(host); err != nil {
			t.Errorf("%s: %v", host, err)
		}
		if ip := net.ParseIP(host); ip != nil && len(cert.IPAddresses) != 1 {
			t.Errorf("%s: want IP SAN", host)
		}
		if !strings.Contains(keyPEM, "PRIVATE KEY") {
			t.Error("no private key")
		}
		pin, err := CertSHA256(certPEM)
		if err != nil || len(pin) != 64 {
			t.Errorf("pin %q, %v", pin, err)
		}
	}
	if _, err := CertSHA256("garbage"); err == nil {
		t.Error("expected error")
	}
}

func TestAllocateWGAddress(t *testing.T) {
	got, err := AllocateWGAddress("10.70.0.0/24", nil)
	if err != nil || got != "10.70.0.2/32" {
		t.Fatalf("first = %q, %v", got, err)
	}
	got, err = AllocateWGAddress("10.70.0.0/24", []string{"10.70.0.2/32", "10.70.0.3"})
	if err != nil || got != "10.70.0.4/32" {
		t.Fatalf("skip taken = %q, %v", got, err)
	}
	// /30: network .0, server .1, client .2, broadcast .3.
	got, err = AllocateWGAddress("10.70.0.0/30", nil)
	if err != nil || got != "10.70.0.2/32" {
		t.Fatalf("/30 = %q, %v", got, err)
	}
	if _, err := AllocateWGAddress("10.70.0.0/30", []string{got}); err == nil {
		t.Error("expected exhaustion error")
	}
	var taken []string
	for i := 2; i <= 254; i++ {
		taken = append(taken, fmt.Sprintf("10.70.0.%d/32", i))
	}
	if _, err := AllocateWGAddress("10.70.0.0/24", taken); err == nil {
		t.Error("expected exhaustion error for full /24")
	}
	for _, bad := range []string{"fd00::/64", "10.70.0.0/31", "nope"} {
		if _, err := AllocateWGAddress(bad, nil); err == nil {
			t.Errorf("%s: expected error", bad)
		}
	}
}

func TestSplitDest(t *testing.T) {
	tests := []struct {
		in   string
		host string
		port uint16
		err  bool
	}{
		{"www.microsoft.com:443", "www.microsoft.com", 443, false},
		{"www.microsoft.com", "www.microsoft.com", 443, false},
		{"example.com:8443", "example.com", 8443, false},
		{"[2001:db8::1]:443", "2001:db8::1", 443, false},
		{"example.com:0", "", 0, true},
		{"example.com:http", "", 0, true},
		{"", "", 0, true},
	}
	for _, tt := range tests {
		host, port, err := splitDest(tt.in)
		if (err != nil) != tt.err || host != tt.host || port != tt.port {
			t.Errorf("splitDest(%q) = %q, %d, %v", tt.in, host, port, err)
		}
	}
}
