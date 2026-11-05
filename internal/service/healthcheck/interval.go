package healthcheck

import "time"

// DefaultHealthInterval is the default health check interval.
const DefaultHealthInterval = 5 * time.Minute

// Interval is how often the nodes are checked: the configured interval, or DefaultHealthInterval when it is not set.
func (s *Service) Interval() time.Duration {
	if s.healthInterval > 0 {
		return s.healthInterval
	}

	return DefaultHealthInterval
}
