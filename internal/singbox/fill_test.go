package singbox

import (
	"reflect"
	"testing"
)

func TestFill(t *testing.T) {
	n := Node{ID: "n1", Host: "n1.example.com", Inbounds: []Inbound{
		{Type: TypeVLESSReality, Port: 443, Reality: &RealityParams{Dest: "www.microsoft.com:443", ServerName: "www.microsoft.com"}},
		{Type: TypeHysteria2, Port: 8443},
		{Type: TypeWireGuard, Port: 51820},
	}}
	// bob already has an address: Fill must not change it or give it to alice.
	users := []User{{Name: "alice"}, {Name: "bob", WG: &WGPeer{Address: "10.70.0.2/32"}}}

	if err := Fill(&n, users); err != nil {
		t.Fatal(err)
	}
	if err := Validate(&n, users); err != nil {
		t.Fatalf("filled data is invalid: %v", err)
	}
	if users[1].WG.Address != "10.70.0.2/32" || users[0].WG.Address != "10.70.0.3/32" {
		t.Errorf("addresses: alice %s, bob %s", users[0].WG.Address, users[1].WG.Address)
	}

	snapshotNode := cloneNode(n)
	snapshotUsers := cloneUsers(users)
	if err := Fill(&n, users); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(n, snapshotNode) || !reflect.DeepEqual(users, snapshotUsers) {
		t.Error("second Fill changed already filled values")
	}
}

func TestFillDerivesWGPublicKey(t *testing.T) {
	priv, pub, err := NewWGKeypair()
	if err != nil {
		t.Fatal(err)
	}
	n := Node{Host: "h", Inbounds: []Inbound{{Type: TypeWireGuard, Port: 51820}}}
	users := []User{{Name: "a", WG: &WGPeer{PrivateKey: priv}}}
	if err := Fill(&n, users); err != nil {
		t.Fatal(err)
	}
	if users[0].WG.PublicKey != pub {
		t.Errorf("public key %q, want %q", users[0].WG.PublicKey, pub)
	}
}

func cloneNode(n Node) Node {
	c := n
	c.Inbounds = nil
	for _, in := range n.Inbounds {
		if in.Reality != nil {
			r := *in.Reality
			r.ShortIDs = append([]string(nil), r.ShortIDs...)
			in.Reality = &r
		}
		if in.Hysteria2 != nil {
			h := *in.Hysteria2
			in.Hysteria2 = &h
		}
		if in.WireGuard != nil {
			w := *in.WireGuard
			in.WireGuard = &w
		}
		c.Inbounds = append(c.Inbounds, in)
	}
	return c
}

func cloneUsers(us []User) []User {
	out := make([]User, len(us))
	for i, u := range us {
		if u.WG != nil {
			p := *u.WG
			u.WG = &p
		}
		out[i] = u
	}
	return out
}
