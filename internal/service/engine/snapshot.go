package engine

// Snapshot returns the current state without waiting for a running operation.
func (r *Runtime) Snapshot() State {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.state
}
