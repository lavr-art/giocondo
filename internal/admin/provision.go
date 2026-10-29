package admin

import (
	"context"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/provision"
	"github.com/artemlavr/giocondo/internal/store"
)

// SSHInstallTimeout is how long an SSH installation may take (apt, ufw, agent start).
const SSHInstallTimeout = 15 * time.Minute

// SSHInput installs the agent on your own server over SSH. The password and key are used once and
// are never stored.
type SSHInput struct {
	Host          string `json:"host"` // empty means the node IP or host
	Port          int    `json:"port"` // 0 — 22
	User          string `json:"user"` // empty means root
	Password      string `json:"password"`
	PrivateKey    string `json:"private_key"`
	HostKeySHA256 string `json:"host_key_sha256"` // "SHA256:…"; empty means trust on first connection
	NoFirewall    bool   `json:"no_firewall"`     // do not enable ufw
	KeepKey       bool   `json:"keep_key"`        // keep the panel key in authorized_keys
}

func (s *Service) remoteNode(ctx context.Context, id int64) (*store.Node, error) {
	n, err := s.Store.GetNode(ctx, id)
	if err != nil {
		return nil, err
	}
	if n.Runtime == store.RuntimeLocal {
		return nil, invalid("node %s runs on the panel machine: use `vpnctl-panel local-node`", n.Name)
	}
	return n, nil
}

// DeployKey is the panel SSH key (PKIDir/ssh/id_ed25519), created on first use.
func (s *Service) DeployKey() (*provision.DeployKey, error) {
	if s.PKIDir == "" {
		return nil, invalid("PKI is not configured (PANEL_PKI_DIR)")
	}
	return provision.LoadOrCreateDeployKey(filepath.Join(s.PKIDir, "ssh"))
}

// CloudInit is user-data for a new server: installs the agent with a new node certificate and ufw.
func (s *Service) CloudInit(ctx context.Context, id int64) ([]byte, string, error) {
	if _, err := s.remoteNode(ctx, id); err != nil {
		return nil, "", err
	}
	files, name, cmd, err := s.agentInstall(ctx, id, true, 22)
	if err != nil {
		return nil, "", err
	}
	data, err := provision.CloudInit(files, cmd)
	if err != nil {
		return nil, "", err
	}
	s.log().Info("cloud-init user-data issued", "node", name)
	return data, name + "-cloud-init.yaml", nil
}

// InstallSSH installs the agent on a server over SSH and waits for completion; progress goes to log.
func (s *Service) InstallSSH(ctx context.Context, id int64, in SSHInput, log io.Writer) error {
	n, err := s.remoteNode(ctx, id)
	if err != nil {
		return err
	}
	host := strings.TrimSpace(in.Host)
	if host == "" {
		host = n.IP
	}
	if host == "" {
		host = n.Host
	}
	if in.Port < 0 || in.Port > 65535 {
		return invalid("port must be 1-65535")
	}
	key, err := s.DeployKey()
	if err != nil {
		return err
	}
	files, name, cmd, err := s.agentInstall(ctx, id, !in.NoFirewall, in.Port)
	if err != nil {
		return err
	}
	bundle, err := provision.TarGz(name, files, s.now())
	if err != nil {
		return err
	}
	target := provision.SSHTarget{Host: host, Port: in.Port, User: strings.TrimSpace(in.User), Password: in.Password,
		PrivateKey: []byte(strings.TrimSpace(in.PrivateKey)), HostKeySHA256: strings.TrimSpace(in.HostKeySHA256)}
	if len(target.PrivateKey) > 0 {
		target.PrivateKey = append(target.PrivateKey, '\n')
	}
	s.log().Info("installing agent over ssh", "node", name, "host", host)
	fp, err := provision.Install(ctx, target, key, provision.InstallOptions{Bundle: bundle, Dir: name, Cmd: cmd, KeepKey: in.KeepKey}, log)
	if err != nil {
		s.log().Error("ssh install failed", "node", name, "host", host, "host_key", fp, "err", err)
		return err
	}
	s.log().Info("agent installed over ssh", "node", name, "host", host, "host_key", fp)
	_, _ = fmt.Fprintf(log, "done: agent installed on %s; the node turns online when the agent connects\n", host)
	// An empty node IP is filled with the address the node was just installed at.
	if n.IP == "" && net.ParseIP(host) != nil {
		n.IP = host
		if err := s.Store.UpdateNode(ctx, n); err != nil {
			s.log().Warn("save node ip", "node", name, "err", err)
		}
	}
	return nil
}

// ProvisionSSH runs InstallSSH in the background and returns the job at once; progress is in Job(id).
func (s *Service) ProvisionSSH(ctx context.Context, id int64, in SSHInput) (provision.JobView, error) {
	if _, err := s.remoteNode(ctx, id); err != nil {
		return provision.JobView{}, err
	}
	// The installation outlives the request: it has its own context with a timeout.
	jctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), SSHInstallTimeout)
	return s.jobs().Start(jctx, id, "ssh", func(ctx context.Context, log *provision.Log) error {
		defer cancel()
		return s.InstallSSH(ctx, id, in, log)
	}), nil
}

// Job is an installation job.
func (s *Service) Job(id string) (provision.JobView, error) {
	if op, e := s.Store.GetNetworkOperation(context.Background(), id); e == nil {
		return provision.JobView{ID: op.ID, NodeID: op.NodeID, Kind: op.Kind, Status: op.Status, Error: op.Error, Log: op.Log, StartedAt: op.StartedAt, FinishedAt: op.FinishedAt}, nil
	}
	j, ok := s.jobs().Get(id)
	if !ok {
		return provision.JobView{}, store.ErrNotFound
	}
	return j, nil
}

func (s *Service) jobs() *provision.Jobs {
	s.jobsOnce.Do(func() { s.jobsList = &provision.Jobs{} })
	return s.jobsList
}
