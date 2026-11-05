package provisioning

import (
	"context"
	"errors"
	"io"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/model/protocol"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// InstallCandidate reports whether the node can be installed.
func (s *Service) InstallCandidate(
	ctx context.Context,
	n *panel.Node,
	i panel.Installation,
	target panel.SSHInput,
	sourceSSH *panel.SSHInput,
	ip string,
) error {
	if _, err := s.monitor.EnsureMonitor(ctx, n.ID); err != nil {
		return err
	}

	if s.remote == nil {
		return operation.Invalid("PKI is not configured (PANEL_PKI_DIR)")
	}

	ins, err := s.nodes.ListInbounds(ctx, n.ID)
	if err != nil {
		return err
	}

	var state []byte

	for _, ib := range ins {
		if ib.Spec.Hysteria2 != nil && ib.Spec.Hysteria2.TLSMode() == protocol.TLSACME {
			if sourceSSH == nil {
				return operation.Invalid("source_ssh required to transfer ACME state")
			}

			source := *sourceSSH
			if source.Host == "" {
				source.Host = n.IP
			}

			state, err = s.remote.ExportACME(ctx, source)
			if err != nil {
				return errors.New("cannot export ACME state from source")
			}
		}
	}

	if target.Host == "" {
		target.Host = ip
	}

	a, err := s.agentInstallIdentity(ctx, n.ID, true, target.Port, i.Identity, state)
	if err != nil {
		return err
	}

	bundle, err := s.artifacts.Archive(a, s.hooks.Now())
	if err != nil {
		return err
	}

	_, err = s.remote.Install(
		ctx,
		target,
		panel.RemoteBundle{Archive: bundle, Directory: a.Name, Command: a.Command, KeepKey: target.KeepKey},
		io.Discard,
	)

	return err
}
