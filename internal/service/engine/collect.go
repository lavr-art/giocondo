package engine

import "context"

// Collect reads and resets the engine counters and passes them to OnTraffic.
func (r *Runtime) Collect(ctx context.Context) {
	r.op.Lock()
	defer r.op.Unlock()

	r.collect(ctx)
}

func (r *Runtime) collect(ctx context.Context) {
	c, cancel := context.WithTimeout(ctx, collectTimeout)
	defer cancel()

	t, err := r.driver.Collect(c)
	if err == nil && r.onTraffic != nil {
		r.onTraffic(t)
	}
}
