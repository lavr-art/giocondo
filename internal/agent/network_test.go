package agent

import (
	"github.com/artemlavr/giocondo/internal/nodeapi/pb"
	"strings"
	"testing"
)

func TestUDPAddressRulesPreserveReplySource(t *testing.T) {
	c := &pb.NetworkConfig{PrimaryIp: "192.0.2.1", Aliases: []string{"192.0.2.1", "192.0.2.2"}, UdpPorts: []uint32{8443, 51820}}
	rules, err := networkRules(c, map[string]bool{"192.0.2.1": true, "192.0.2.2": true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-d 192.0.2.2 -p udp --dport 8443 -j DNAT --to-destination 192.0.2.1", "-d 192.0.2.2 -p udp --dport 51820"} {
		if !strings.Contains(rules, want) {
			t.Fatal(rules)
		}
	}
	if strings.Contains(rules, "-d 192.0.2.1 ") {
		t.Fatal("canonical address must not be translated")
	}
	c.Aliases = []string{"192.0.2.2; bad"}
	if _, err = networkRules(c, map[string]bool{}); err == nil {
		t.Fatal("invalid IP accepted")
	}
}
