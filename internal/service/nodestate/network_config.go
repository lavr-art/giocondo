package nodestate

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// NetworkConfig returns the addresses of the installation in use; ok is false until the node has
// an active installation with a primary address.
func (s *Service) NetworkConfig(ctx context.Context, a *Agent) (Network, bool, error) {
	current, err := s.nodes.GetNode(ctx, a.Node.ID)
	if err != nil || current.ActiveInstallation == 0 {
		return Network{}, false, err
	}

	inst := a.Installation
	if inst == nil {
		if inst, err = s.network.InstallationByIdentity(ctx, a.Identity); err != nil {
			return Network{}, false, err
		}
	}

	addresses, err := s.network.ListAddresses(ctx, a.Node.ID)
	if err != nil {
		return Network{}, false, err
	}

	network := Network{PrimaryIP: inst.IP}
	for _, addr := range addresses {
		if addr.InstallationID == inst.ID {
			network.Aliases = append(network.Aliases, addr.IP)
		}
	}

	inbounds, err := s.nodes.ListInbounds(ctx, a.Node.ID)
	if err != nil {
		return Network{}, false, err
	}

	for _, in := range inbounds {
		if in.Spec.Type != protocol.TypeVLESSReality {
			network.UDPPorts = append(network.UDPPorts, uint32(in.Spec.Port))
		}
	}

	if network.PrimaryIP == "" {
		return Network{}, false, nil
	}

	return network, true, nil
}
