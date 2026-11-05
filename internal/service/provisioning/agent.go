package provisioning

import (
	"context"
	"strings"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/pkg/logger"
)

// AgentAddress returns the configured panel address included in agent installations.
func (s *Service) AgentAddress() string { return s.agentAddr }

// AptRepoURL returns the apt repository URL.
func (s *Service) AptRepoURL() string {
	if s.aptRepo != "" {
		return strings.TrimSuffix(s.aptRepo, "/")
	}

	return DefaultAptRepo
}

func (s *Service) remoteNode(ctx context.Context, id int64) (*panel.Node, error) {
	n, err := s.nodes.GetNode(ctx, id)
	if err != nil {
		return nil, err
	}

	if n.Runtime == panel.RuntimeLocal {
		return nil, operation.Invalid("node %s runs on the panel machine: use `giocondo-vpn-panel local-node`", n.Name)
	}

	return n, nil
}

func (s *Service) issueIdentity(ctx context.Context, identity string) (panel.AgentCert, string, error) {
	if s.issuer == nil {
		return panel.AgentCert{}, "", operation.Invalid("PKI is not configured (PANEL_PKI_DIR)")
	}

	c, err := s.issuer.Issue(ctx, identity)
	if err != nil {
		return panel.AgentCert{}, "", err
	}

	c.PanelAddr = s.agentAddr

	logger.FromCtx(ctx).Info("agent certificate issued", "node", identity)

	return c, identity, nil
}

func (s *Service) agentInstall(
	ctx context.Context,
	id int64,
	firewall bool,
	sshPort int,
) (panel.InstallArtifact, error) {
	return s.agentInstallIdentity(ctx, id, firewall, sshPort, "", nil)
}

func (s *Service) agentInstallIdentity(
	ctx context.Context,
	id int64,
	firewall bool,
	sshPort int,
	identity string,
	acmeState []byte,
) (panel.InstallArtifact, error) {
	var (
		c    panel.AgentCert
		name string
		err  error
	)
	if identity == "" {
		c, name, err = s.IssueAgentCert(ctx, id)
	} else {
		c, name, err = s.issueIdentity(ctx, identity)
	}

	if err != nil {
		return panel.InstallArtifact{}, err
	}

	ins, err := s.nodes.ListInbounds(ctx, id)
	if err != nil {
		return panel.InstallArtifact{}, err
	}

	if s.artifacts == nil {
		return panel.InstallArtifact{}, operation.Invalid("installation artifacts are not configured")
	}

	spec := panel.InstallSpec{
		Name:      name,
		Cert:      c,
		Repo:      s.AptRepoURL(),
		Firewall:  firewall,
		SSHPort:   sshPort,
		ACMEState: acmeState,
	}
	for _, in := range ins {
		spec.Inbounds = append(spec.Inbounds, in.Spec)
	}

	return s.artifacts.Build(spec)
}

// DefaultAptRepo is the default apt repository URL.
const DefaultAptRepo = "https://lavr-art.github.io/giocondo"
