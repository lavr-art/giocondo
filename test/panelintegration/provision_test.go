package panelintegration

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/clients/ssh"
	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/platform/installation"
	"github.com/artemlavr/giocondo/internal/repository/pkifs"
	"github.com/artemlavr/giocondo/internal/repository/sqlite"
	"github.com/artemlavr/giocondo/internal/service/pki"
	"github.com/artemlavr/giocondo/internal/service/provisioning"
	"github.com/artemlavr/giocondo/test/paneltest"
)

func provisionService(t *testing.T) (*paneltest.Components, panel.NodeView) {
	t.Helper()

	dir := t.TempDir()
	pkiDir := filepath.Join(dir, "pki")
	if err := (pki.Certificates{Store: pkifs.Disk{Dir: pkiDir}}).InitCA(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(context.Background(), filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := paneltest.New(paneltest.Config{AgentAddr: "panel.example.com:9443"}, paneltest.Dependencies{DB: st, Issuer: pki.Certificates{Store: pkifs.Disk{Dir: pkiDir}}, Artifacts: installation.Artifacts{}, Remote: ssh.Remote{PKIDir: pkiDir}})
	t.Cleanup(func() { _ = svc.Close() })
	n, err := svc.Nodes.CreateNode(context.Background(), panel.CreateNodeInput{Name: "n1", Host: "n1.example.com", VLESS: &panel.VLESSInput{}, Hysteria2: &panel.Hy2Input{Port: 8443}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, n
}

func TestCloudInitForNode(t *testing.T) {
	ctx := context.Background()
	svc, n := provisionService(t)
	data, name, err := svc.Provisioning.CloudInit(ctx, n.ID)
	if err != nil {
		t.Fatal(err)
	}

	y := string(data)
	for _, want := range []string{"#cloud-config", "/root/giocondo-vpn/agent.key", "--panel", "panel.example.com:9443", "--firewall"} {
		if !strings.Contains(y, want) {
			t.Errorf("cloud-init lacks %q", want)
		}
	}
	if !strings.Contains(y, "443/tcp,8443/udp") || name != "n1-cloud-init.yaml" {
		t.Errorf("firewall ports or name: %s\n%s", name, y)
	}

	local, err := svc.Nodes.CreateNode(ctx, panel.CreateNodeInput{Name: "local", Host: "vpn.example.com", Hysteria2: &panel.Hy2Input{}, Runtime: panel.RuntimeLocal})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Provisioning.CloudInit(ctx, local.ID); !errors.Is(err, panel.ErrInvalid) {
		t.Errorf("local node: %v", err)
	}
	if _, err := svc.Provisioning.ProvisionSSH(ctx, local.ID, panel.SSHInput{}); !errors.Is(err, panel.ErrInvalid) {
		t.Errorf("ssh to local node: %v", err)
	}
}

func TestProvisionSSHJobFails(t *testing.T) {
	ctx := context.Background()
	svc, n := provisionService(t)
	lis, _ := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	addr := lis.Addr().(*net.TCPAddr)
	_ = lis.Close() // nobody is listening
	job, err := svc.Provisioning.ProvisionSSH(ctx, n.ID, panel.SSHInput{Host: "127.0.0.1", Port: addr.Port, Password: "pw-secret"})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		j, err := svc.Provisioning.Job(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status != provisioning.JobRunning {
			if j.Status != provisioning.JobFailed || j.Error == "" || strings.Contains(j.Log+j.Error, "pw-secret") {
				t.Errorf("job %+v", j)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := svc.Provisioning.Job(context.Background(), "nope"); !errors.Is(err, panel.ErrNotFound) {
		t.Errorf("unknown job: %v", err)
	}
	key, err := svc.Provisioning.DeployKey(context.Background())
	if err != nil || !strings.HasSuffix(key.AuthorizedKey, " giocondo-vpn-panel") {
		t.Errorf("deploy key %v %v", key, err)
	}
}
