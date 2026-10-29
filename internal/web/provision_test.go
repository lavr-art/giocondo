package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/admin"
)

func TestNodeProvisioningPages(t *testing.T) {
	e := newEnv(t, testPassword)
	c := e.client(t)
	c.login()
	n, err := e.svc.CreateNode(context.Background(), admin.CreateNodeInput{Name: "n1", Host: "n1.example.com", Hysteria2: &admin.Hy2Input{}})
	if err != nil {
		t.Fatal(err)
	}
	nodePath := "/nodes/" + itoa(n.ID)

	_, body := c.do("GET", nodePath, nil, nil)
	for _, want := range []string{"Download user-data", "provision-ssh", "ssh-ed25519 ", "vpnctl-panel"} {
		if !strings.Contains(body, want) {
			t.Errorf("node page lacks %q", want)
		}
	}
	resp, body := c.do("POST", nodePath+"/cloud-init", nil, nil)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(body, "#cloud-config") ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), "n1-cloud-init.yaml") {
		t.Fatalf("cloud-init: %d %v", resp.StatusCode, resp.Header)
	}

	// Port 1 is closed: the job starts and fails, and the page shows it.
	resp, _ = c.do("POST", nodePath+"/provision-ssh", url.Values{"host": {"127.0.0.1"}, "port": {"1"}, "password": {"pw"}, "firewall": {"on"}}, nil)
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(loc, "?job=") {
		t.Fatalf("provision: %d %s", resp.StatusCode, loc)
	}
	jobID := loc[strings.Index(loc, "?job=")+5:]
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, part := c.do("GET", "/jobs/"+jobID, nil, map[string]string{"HX-Request": "true"})
		if !strings.Contains(part, `hx-trigger="every 2s"`) {
			if !strings.Contains(part, "error") {
				t.Errorf("finished job block: %s", part)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, body := c.do("GET", loc, nil, nil); !strings.Contains(body, `id="job"`) {
		t.Error("node page does not show the job")
	}
	if resp, _ := c.do("POST", nodePath+"/provision-ssh", url.Values{"port": {"abc"}}, nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad port: %d", resp.StatusCode)
	}
}
