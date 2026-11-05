package engine

import "context"

// Refresh updates the reported activity of the engine. It is skipped while another operation runs,
// since that operation refreshes the state itself.
func (r *Runtime) Refresh(ctx context.Context) {
	if !r.op.TryLock() {
		return
	}
	defer r.op.Unlock()

	r.refresh(ctx)
}

func (r *Runtime) refresh(ctx context.Context) {
	c, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()

	active, err := r.driver.Active(c)
	r.setState(func(s *State) {
		s.Active = active
		if err != nil && s.Error == "" {
			s.Error = "engine status check failed"
		} else if err == nil && s.Phase != phaseBlocked && s.Error == "engine status check failed" {
			s.Error = ""
		}
	})
}
