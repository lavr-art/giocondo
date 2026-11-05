package operation

import (
	"fmt"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
)

// Hooks are the effects shared by the services: the clock, waking Sync after a change of the desired
// node state, and event notifications. The application builds one Hooks for all services. Services log
// through logger.FromCtx.
type Hooks struct {
	Now      func() time.Time // UTC
	Kick     func()
	Emit     func(panel.Event)
	EmitUser func(string, panel.UserView)
}

// Invalid wraps an input validation error.
func Invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", panel.ErrInvalid, fmt.Sprintf(format, args...))
}
