package provisioning

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// InstallSSH installs the agent on a server over SSH and waits for completion; progress goes to log.
func (s *Service) InstallSSH(ctx context.Context, id int64, in panel.SSHInput, log io.Writer) error {
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
		return operation.Invalid("port must be 1-65535")
	}

	if s.remote == nil {
		return operation.Invalid("PKI is not configured (PANEL_PKI_DIR)")
	}

	a, err := s.agentInstall(ctx, id, !in.NoFirewall, in.Port)
	if err != nil {
		return err
	}

	bundle, err := s.artifacts.Archive(a, s.hooks.Now())
	if err != nil {
		return err
	}

	in.Host = host
	in.User = strings.TrimSpace(in.User)
	name := a.Name
	logger.FromCtx(ctx).Info("installing agent over ssh", "node", name, "host", host)

	fp, err := s.remote.Install(
		ctx,
		in,
		panel.RemoteBundle{Archive: bundle, Directory: name, Command: a.Command, KeepKey: in.KeepKey},
		log,
	)
	if err != nil {
		return fmt.Errorf("install agent on %s: %w", host, err)
	}

	logger.FromCtx(ctx).Info("agent installed over ssh", "node", name, "host", host, "host_key", fp)
	_, _ = fmt.Fprintf(log, "done: agent installed on %s; the node turns online when the agent connects\n", host)

	// An empty node IP is filled with the address the node was just installed at.
	if n.IP == "" && net.ParseIP(host) != nil {
		n.IP = host
		if err := s.nodes.UpdateNode(ctx, n); err != nil {
			// The agent is installed, so the install succeeds; the operator sets the IP by hand.
			logger.FromCtx(ctx).Warn("save node ip", "node", name, "err", err)

			_, _ = fmt.Fprintf(log, "warning: node IP %s was not saved; set it on the node page\n", host)
		}
	}

	return nil
}

// SSHInstallTimeout is how long an SSH installation may take (apt, ufw, agent start).
const SSHInstallTimeout = 15 * time.Minute
