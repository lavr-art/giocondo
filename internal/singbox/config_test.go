package singbox

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

var update = flag.Bool("update", false, "rewrite golden files")

type testFile struct {
	Node  Node   `yaml:"node"`
	Users []User `yaml:"users"`
}

func loadTestFile(t *testing.T, name string) testFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var f testFile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestBuildGolden(t *testing.T) {
	singBox, _ := exec.LookPath("sing-box")
	for _, name := range []string{"vless", "hysteria2-selfsigned", "hysteria2-acme", "wireguard", "all"} {
		t.Run(name, func(t *testing.T) {
			f := loadTestFile(t, name)
			cfg, err := Build(&f.Node, f.Users, Options{})
			if err != nil {
				t.Fatal(err)
			}
			got, err := cfg.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", name+".golden.json")
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run go test -update)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("config differs from %s (run go test -update and review the diff)", golden)
			}

			if singBox == "" {
				t.Log("sing-box not in PATH, skipping sing-box check")
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if out, err := exec.CommandContext(ctx, singBox, "check", "-c", golden).CombinedOutput(); err != nil {
				t.Fatalf("sing-box check: %v\n%s", err, out)
			}
		})
	}
}

func TestBuildIsOrderIndependent(t *testing.T) {
	f := loadTestFile(t, "all")
	a, err := Build(&f.Node, f.Users, Options{})
	if err != nil {
		t.Fatal(err)
	}
	reversed := make([]User, len(f.Users))
	for i, u := range f.Users {
		reversed[len(f.Users)-1-i] = u
	}
	b, err := Build(&f.Node, reversed, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ja, _ := a.Marshal()
	jb, _ := b.Marshal()
	if !bytes.Equal(ja, jb) {
		t.Error("config depends on user order")
	}
	if f.Users[0].Name != "artem" {
		t.Error("Build must not reorder the caller's slice")
	}
}

// The config structure the agent and traffic accounting rely on.
func TestBuildInvariants(t *testing.T) {
	f := loadTestFile(t, "all")
	cfg, err := Build(&f.Node, f.Users, Options{V2RayAPIListen: "127.0.0.1:9999"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := cfg.Marshal()
	var m struct {
		Route struct {
			Rules []map[string]any `json:"rules"`
			Final string           `json:"final"`
		} `json:"route"`
		Experimental struct {
			V2RayAPI struct {
				Listen string `json:"listen"`
				Stats  struct {
					Users     []string `json:"users"`
					Outbounds []string `json:"outbounds"`
				} `json:"stats"`
			} `json:"v2ray_api"`
		} `json:"experimental"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	rules := m.Route.Rules
	if len(rules) < 2 || rules[0]["action"] != "resolve" || rules[1]["action"] != "reject" || rules[1]["ip_is_private"] != true {
		t.Errorf("first rules must be resolve + reject private, got %v", rules)
	}
	if m.Route.Final != "direct" {
		t.Errorf("final = %q", m.Route.Final)
	}
	api := m.Experimental.V2RayAPI
	if api.Listen != "127.0.0.1:9999" {
		t.Errorf("v2ray_api listen = %q", api.Listen)
	}
	if strings.Join(api.Stats.Users, ",") != "artem,test1" {
		t.Errorf("stats users = %v", api.Stats.Users)
	}
	if strings.Join(api.Stats.Outbounds, ",") != "wg-artem,wg-test1" {
		t.Errorf("stats outbounds = %v", api.Stats.Outbounds)
	}
}

func TestWireGuardClientConfig(t *testing.T) {
	f := loadTestFile(t, "wireguard")
	conf, err := WireGuardClientConfig(&f.Node, f.Users[0])
	if err != nil {
		t.Fatal(err)
	}
	serverPub, _ := WGPublicKey(f.Node.Inbounds[0].WireGuard.PrivateKey)
	for _, want := range []string{
		"PrivateKey = " + f.Users[0].WG.PrivateKey,
		"Address = " + f.Users[0].WG.Address,
		"MTU = 1380",
		"PublicKey = " + serverPub,
		"PresharedKey = " + f.Users[0].WG.PresharedKey,
		"Endpoint = 203.0.113.10:51820",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("client config has no %q:\n%s", want, conf)
		}
	}
	vless := loadTestFile(t, "vless")
	if _, err := WireGuardClientConfig(&vless.Node, f.Users[0]); err == nil {
		t.Error("expected error for node without wireguard")
	}
}
