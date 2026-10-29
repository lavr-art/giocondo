package admin

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/nodeapi/pki"
	"github.com/artemlavr/giocondo/internal/provision"
	"github.com/artemlavr/giocondo/internal/store"
)

func provisionService(t *testing.T) (*Service, NodeView) {
	t.Helper()
	svc, _ := newService(t, newClock(time.Now()))
	svc.PKIDir = filepath.Join(t.TempDir(), "pki")
	svc.AgentAddr = "panel.example.com:9443"
	if err := pki.InitCA(svc.PKIDir); err != nil {
		t.Fatal(err)
	}
	n, err := svc.CreateNode(context.Background(), CreateNodeInput{Name: "n1", Host: "n1.example.com",
		VLESS: &VLESSInput{}, Hysteria2: &Hy2Input{Port: 8443}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, n
}

func TestCloudInitForNode(t *testing.T) {
	ctx := context.Background()
	svc, n := provisionService(t)
	data, name, err := svc.CloudInit(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}
	y := string(data)
	for _, want := range []string{"#cloud-config", "/root/vpnctl/agent.key", "--panel", "panel.example.com:9443", "--firewall"} {
		if !strings.Contains(y, want) {
			t.Errorf("cloud-init lacks %q", want)
		}
	}
	if !strings.Contains(y, "443/tcp,8443/udp") || name != "n1-cloud-init.yaml" {
		t.Errorf("firewall ports or name: %s\n%s", name, y)
	}

	local, err := svc.CreateNode(ctx, CreateNodeInput{Name: "local", Host: "vpn.example.com", Hysteria2: &Hy2Input{}, Runtime: store.RuntimeLocal})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.CloudInit(ctx, local.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("local node: %v", err)
	}
	if _, err := svc.ProvisionSSH(ctx, local.ID, SSHInput{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("ssh to local node: %v", err)
	}
}

func TestProvisionSSHJobFails(t *testing.T) {
	ctx := context.Background()
	svc, n := provisionService(t)
	lis, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := lis.Addr().(*net.TCPAddr)
	_ = lis.Close() // nobody is listening
	job, err := svc.ProvisionSSH(ctx, n.ID, SSHInput{Host: "127.0.0.1", Port: addr.Port, Password: "pw-secret"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		j, err := svc.Job(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status != provision.JobRunning {
			if j.Status != provision.JobFailed || j.Error == "" || strings.Contains(j.Log+j.Error, "pw-secret") {
				t.Errorf("job %+v", j)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := svc.Job("nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown job: %v", err)
	}
	key, err := svc.DeployKey()
	if err != nil || !strings.HasSuffix(key.AuthorizedKey, " vpnctl-panel") {
		t.Errorf("deploy key %v %v", key, err)
	}
}
