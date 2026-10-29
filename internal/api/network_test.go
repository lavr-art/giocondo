package api

import (
	"context"
	"github.com/artemlavr/giocondo/internal/admin"
	"net/http"
	"strconv"
	"testing"
)

func TestNetworkRoutesRequireAdmin(t *testing.T) {
	e := newEnv(t)
	n, err := e.svc.CreateNode(context.Background(), admin.CreateNodeInput{Name: "network", Host: "192.0.2.1", IP: "192.0.2.1", VLESS: &admin.VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/nodes/" + strconv.FormatInt(n.ID, 10)
	monitor := e.newToken(t, admin.ScopeMonitor)
	sync := e.newToken(t, admin.ScopeNodeSync)
	if status := e.doAs(t, monitor, "GET", path+"/addresses", "", nil); status != http.StatusOK {
		t.Fatalf("GET %d", status)
	}
	for _, token := range []string{monitor, sync} {
		if status := e.doAs(t, token, "POST", path+"/addresses", `{"ip":"192.0.2.2"}`, nil); status != http.StatusForbidden {
			t.Fatalf("POST %d", status)
		}
	}
	if status := e.do(t, "POST", path+"/addresses", `{"ip":"192.0.2.2"}`, nil); status != http.StatusCreated {
		t.Fatalf("POST %d", status)
	}
}
