package panelintegration

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/provisioning"
	. "github.com/artemlavr/giocondo/test/paneltest" //nolint:revive // dot import keeps the test package call sites concise.
)

type fixtureIssuer struct{}

func (fixtureIssuer) Issue(context.Context, string) (panel.AgentCert, error) {
	return panel.AgentCert{CACert: "ca", AgentCert: "certificate", AgentKey: "secret-key"}, nil
}

type fixtureArtifacts struct{}

func (fixtureArtifacts) Build(s panel.InstallSpec) (panel.InstallArtifact, error) {
	return panel.InstallArtifact{Name: s.Name, Command: "install"}, nil
}

func (fixtureArtifacts) Archive(panel.InstallArtifact, time.Time) ([]byte, error) {
	return []byte("fixture-archive"), nil
}

func (fixtureArtifacts) CloudInit(panel.InstallArtifact) ([]byte, error) { return nil, nil }

type waitingRemote struct {
	entered chan struct{}
	release chan struct{}
}

func (r *waitingRemote) Install(ctx context.Context, _ panel.SSHInput, _ panel.RemoteBundle, w io.Writer) (string, error) {
	close(r.entered)
	<-r.release
	if err := ctx.Err(); err != nil {
		return "fingerprint", err
	}
	_, err := io.WriteString(w, "installed\n")
	return "fingerprint", err
}

func (*waitingRemote) ExportACME(context.Context, panel.SSHInput) ([]byte, error) {
	return nil, nil
}

func (*waitingRemote) StartServices(context.Context, panel.SSHInput) error { return nil }
func (*waitingRemote) StopServices(context.Context, panel.SSHInput) error  { return nil }

func (*waitingRemote) PublicDeployKey(context.Context) (panel.DeployKeyView, error) {
	return panel.DeployKeyView{AuthorizedKey: "public"}, nil
}

func TestSSHJobOutlivesRequestCancellation(t *testing.T) {
	s, _ := newService(t, newClock(time.Now()))
	remote := &waitingRemote{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-remote.release:
		default:
			close(remote.release)
		}
	})
	s.configure(t, func(_ *Config, d *Dependencies) {
		d.Issuer = fixtureIssuer{}
		d.Artifacts = fixtureArtifacts{}
		d.Remote = remote
	})
	n, err := s.Nodes.CreateNode(context.Background(), panel.CreateNodeInput{Name: "n", Host: "vpn.example.com", VLESS: &panel.VLESSInput{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	job, err := s.Provisioning.ProvisionSSH(ctx, n.ID, panel.SSHInput{Password: "password-not-for-journal"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-remote.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("SSH installation did not start")
	}
	cancel()
	close(remote.release)
	deadline := time.Now().Add(3 * time.Second)
	for {
		j, err := s.Provisioning.Job(context.Background(), job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status != provisioning.JobRunning {
			if j.Status != provisioning.JobOK || j.Error != "" || j.FinishedAt == nil || j.Log != "installed\ndone: agent installed on vpn.example.com; the node turns online when the agent connects\n" {
				t.Fatalf("job after request cancellation: %+v", j)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("job did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRecoveryKeepsJournalAndRespectsExternalDNS(t *testing.T) {
	for _, address := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.99"} {
		t.Run(address, func(t *testing.T) {
			s, _ := newService(t, newClock(time.Now()))
			dns := &memoryDNS{}
			s.configure(t, func(c *Config, d *Dependencies) { c.DNSDomain = "vpn.example.com"; d.DNS = dns })
			ctx := context.Background()
			n, err := s.Nodes.CreateNode(ctx, panel.CreateNodeInput{Name: "n", Host: "192.0.2.1", IP: "192.0.2.1", VLESS: &panel.VLESSInput{}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Network.EnableDNS(ctx, n.ID, true); err != nil {
				t.Fatal(err)
			}
			node, err := s.Store.Nodes.GetNode(ctx, n.ID)
			if err != nil {
				t.Fatal(err)
			}
			op := panel.NetworkOperation{ID: "interrupted", NodeID: node.ID, Kind: "existing_ip", OldIP: "192.0.2.1", NewIP: "192.0.2.2", OldInstallation: node.ActiveInstallation, NewInstallation: node.ActiveInstallation}
			if err = s.Store.Network.CreateNetworkOperation(ctx, &op); err != nil {
				t.Fatal(err)
			}
			dns.r.Content = address
			err = s.Network.RecoverNetworkOperations(ctx)
			journal, e := s.Store.Network.GetNetworkOperation(ctx, op.ID)
			if e != nil {
				t.Fatal(e)
			}
			current, e := s.Store.Nodes.GetNode(ctx, n.ID)
			if e != nil {
				t.Fatal(e)
			}
			if address == "192.0.2.99" {
				if err == nil || journal.Status != "running" || current.IP != "192.0.2.1" || dns.r.Content != address {
					t.Fatalf("external DNS overwritten or journal unlocked: %+v %+v %v", journal, current, err)
				}
			} else {
				if err != nil || journal.Status != "failed" || journal.FinishedAt == nil || current.IP != address {
					t.Fatalf("recovered state: %+v %+v %v", journal, current, err)
				}
				if address == "192.0.2.2" && journal.Stage != "switched" {
					t.Fatalf("lost switched stage: %+v", journal)
				}
			}
		})
	}
}
