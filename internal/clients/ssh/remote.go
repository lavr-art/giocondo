package ssh

import (
	"context"
	"io"
	"path/filepath"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// Remote executes commands on a remote host.
type Remote struct{ PKIDir string }

func sshTarget(in panel.SSHInput) Target {
	key := strings.TrimSpace(in.PrivateKey)
	if key != "" {
		key += "\n"
	}

	return Target{
		Host:          in.Host,
		Port:          in.Port,
		User:          in.User,
		Password:      in.Password,
		PrivateKey:    []byte(key),
		HostKeySHA256: in.HostKeySHA256,
	}
}

// Install installs the agent on a node.
func (r Remote) Install(
	ctx context.Context,
	in panel.SSHInput,
	b panel.RemoteBundle,
	w io.Writer,
) (string, error) {
	key, err := r.key()
	if err != nil {
		return "", err
	}

	return Install(
		ctx,
		sshTarget(in),
		key,
		InstallOptions{Bundle: b.Archive, Dir: b.Directory, Cmd: b.Command, KeepKey: b.KeepKey},
		w,
	)
}

// ExportACME exports ACME state from a node.
func (r Remote) ExportACME(ctx context.Context, in panel.SSHInput) ([]byte, error) {
	key, err := r.key()
	if err != nil {
		return nil, err
	}

	return ExportACME(ctx, sshTarget(in), key)
}

// StartServices starts the node services.
func (r Remote) StartServices(ctx context.Context, in panel.SSHInput) error {
	key, err := r.key()
	if err != nil {
		return err
	}

	return StartServices(ctx, sshTarget(in), key)
}

// StopServices stops the node services.
func (r Remote) StopServices(ctx context.Context, in panel.SSHInput) error {
	key, err := r.key()
	if err != nil {
		return err
	}

	return StopServices(ctx, sshTarget(in), key)
}

// PublicDeployKey returns the public part of a deploy key.
func (r Remote) PublicDeployKey(ctx context.Context) (panel.DeployKeyView, error) {
	if err := ctx.Err(); err != nil {
		return panel.DeployKeyView{}, err
	}

	key, err := r.key()
	if err != nil {
		return panel.DeployKeyView{}, err
	}

	return panel.DeployKeyView{AuthorizedKey: key.AuthorizedKey}, nil
}

func (r Remote) key() (*DeployKey, error) {
	return LoadOrCreateDeployKey(filepath.Join(r.PKIDir, "ssh"))
}
