package nodeprobe

import (
	"context"

	"github.com/artemlavr/giocondo/internal/model/probe"
	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// CheckProtocol validates protocol settings.
func (s *Service) CheckProtocol(ctx context.Context, c probe.Checker, r probe.Request) error {
	if r.Inbound.Type == protocol.TypeWireGuard {
		s.wgProbeMu.Lock()
		defer s.wgProbeMu.Unlock()
	}

	return c.Check(ctx, r)
}
