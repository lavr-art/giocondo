package provisioning

import "context"

// AgentBundle builds the agent bundle for a node.
func (s *Service) AgentBundle(ctx context.Context, id int64) ([]byte, string, error) {
	a, err := s.agentInstall(ctx, id, false, 0)
	if err != nil {
		return nil, "", err
	}

	data, err := s.artifacts.Archive(a, s.hooks.Now())
	if err != nil {
		return nil, "", err
	}

	return data, a.Name + "-agent.tar.gz", nil
}
