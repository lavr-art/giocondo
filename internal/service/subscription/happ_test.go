package subscription

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestHappRoutingLinkGolden(t *testing.T) {
	link, err := HappRoutingLink()
	if err != nil {
		t.Fatal(err)
	}

	encoded, ok := strings.CutPrefix(link, "happ://routing/onadd/")
	if !ok {
		t.Fatalf("link %q has no activating prefix", link)
	}

	profile, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("profile is not base64: %v", err)
	}

	var pretty bytes.Buffer
	if err := json.Indent(&pretty, profile, "", "  "); err != nil {
		t.Fatalf("profile is not JSON: %v", err)
	}

	pretty.WriteByte('\n')
	checkGolden(t, "happ-routing.json", pretty.Bytes())
}

func TestIsHapp(t *testing.T) {
	tests := []struct {
		userAgent string
		want      bool
	}{
		{"Happ/4.7.2/macOS/2610301208604", true},
		{"Happ/1.9.2", true},
		{"happ/1.9.2", true},
		{"v2RayTun/2.0", false},
		{"HiddifyNext/4.0.0(darwin) like ClashMeta v2ray sing-box", false},
		{"Unhappy/1.0", false},
		{"Happ", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsHapp(tt.userAgent); got != tt.want {
			t.Errorf("IsHapp(%q) = %t, want %t", tt.userAgent, got, tt.want)
		}
	}
}
