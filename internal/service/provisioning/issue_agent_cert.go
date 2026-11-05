package provisioning

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
)

// IssueAgentCert issues a certificate for an agent.
func (s *Service) IssueAgentCert(ctx context.Context, id int64) (panel.AgentCert, string, error) {
	n, err := s.nodes.GetNode(ctx, id)
	if err != nil {
		return panel.AgentCert{}, "", err
	}

	if s.issuer == nil {
		return panel.AgentCert{}, "", operation.Invalid("PKI is not configured (PANEL_PKI_DIR)")
	}

	identity := n.Name
	if n.ActiveInstallation != 0 {
		ins, e := s.network.Installations(ctx, id)
		if e != nil {
			return panel.AgentCert{}, "", e
		}

		for _, i := range ins {
			if i.ID == n.ActiveInstallation {
				identity = i.Identity
			}
		}
	}

	return s.issueIdentity(ctx, identity)
}
