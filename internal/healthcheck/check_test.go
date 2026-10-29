package healthcheck

import (
	"encoding/json"
	"github.com/artemlavr/giocondo/internal/singbox"
	"testing"
)

func TestClientConfigUsesIPAndStableTLS(t *testing.T) {
	priv, _, _ := singbox.NewWGKeypair()
	peerPriv, peerPub, _ := singbox.NewWGKeypair()
	_ = peerPriv
	cases := []singbox.Inbound{
		{Type: singbox.TypeHysteria2, Port: 8443, Hysteria2: &singbox.Hysteria2Params{ObfsPassword: "obfs"}},
		{Type: singbox.TypeWireGuard, Port: 51820, WireGuard: &singbox.WireGuardParams{PrivateKey: priv}},
	}
	for _, in := range cases {
		b, err := ClientConfig(Request{IP: "192.0.2.1", Host: "n1.example.com", Inbound: in, User: singbox.User{UUID: "test", WG: &singbox.WGPeer{PrivateKey: priv, PublicKey: peerPub, Address: "10.70.0.254/32"}}}, 12345)
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err = json.Unmarshal(b, &v); err != nil {
			t.Fatal(err)
		}
		if in.Type == singbox.TypeHysteria2 {
			out := v["outbounds"].([]any)[0].(map[string]any)
			if out["server"] != "192.0.2.1" || out["tls"].(map[string]any)["server_name"] != "n1.example.com" {
				t.Fatal(out)
			}
			if out["obfs"].(map[string]any)["password"] != "obfs" {
				t.Fatal(out)
			}
		} else if len(v["endpoints"].([]any)) != 1 {
			t.Fatal(v)
		}
	}
}
