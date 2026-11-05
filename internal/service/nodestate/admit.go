package nodestate

import (
	"context"
	"errors"
	"fmt"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// Admit identifies the agent and refuses unknown, disabled and retired installations.
func (s *Service) Admit(ctx context.Context, identity string) (*Agent, error) {
	installation, installErr := s.network.InstallationByIdentity(ctx, identity)

	var (
		node *panel.Node
		err  error
	)

	switch {
	case installErr == nil:
		node, err = s.nodes.GetNode(ctx, installation.NodeID)
	case errors.Is(installErr, panel.ErrNotFound):
		installation = nil
		node, err = s.findNode(ctx, identity)
	default:
		err = installErr
	}

	if installErr == nil && installation.State == panel.InstallationRetired {
		return nil, ErrRetired
	}

	switch {
	case errors.Is(err, panel.ErrNotFound):
		return nil, fmt.Errorf("%w: %q", ErrNotRegistered, identity)
	case err != nil:
		return nil, err
	case node.Status == panel.NodeDisabled:
		return nil, fmt.Errorf("%w: %q", ErrDisabled, identity)
	}

	return &Agent{Identity: identity, Node: node, Installation: installation}, nil
}

func (s *Service) findNode(ctx context.Context, name string) (*panel.Node, error) {
	nodes, err := s.nodes.ListNodes(ctx)
	if err != nil {
		return nil, err
	}

	for _, n := range nodes {
		if n.Name == name {
			return &n, nil
		}
	}

	return nil, panel.ErrNotFound
}
