package nodestate

import (
	"context"
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// Admission errors. Other errors returned by Admit come from the store.
var (
	ErrNotRegistered = errors.New("node is not registered")
	ErrDisabled      = errors.New("node is disabled")
	ErrRetired       = errors.New("installation retired")
)

// stateWriteTimeout bounds state writes detached from the stream context, which is already
// cancelled when the stream closes.
const stateWriteTimeout = 5 * time.Second

// currentInstallation resolves the installation lazily: it may be registered while the stream is open.
func (s *Service) currentInstallation(ctx context.Context, a *Agent) *panel.Installation {
	if a.Installation == nil {
		if i, err := s.network.InstallationByIdentity(ctx, a.Identity); err == nil {
			a.Installation = i
		}
	}

	return a.Installation
}

func (s *Service) now() time.Time {
	if s.hooks.Now != nil {
		return s.hooks.Now()
	}

	return time.Now().UTC()
}
