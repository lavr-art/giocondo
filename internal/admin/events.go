package admin

import "time"

// Event types (the event field of a webhook).
const (
	EventUserCreated        = "user_created"
	EventUserUpdated        = "user_updated"
	EventUserDeleted        = "user_deleted"
	EventUserLimited        = "user_limited"         // traffic is exhausted
	EventUserExpired        = "user_expired"         // the term has expired
	EventUserTrafficWarning = "user_traffic_warning" // TrafficWarnPercent% of the limit is used
	EventUserExpiring       = "user_expiring"        // less than ExpiryWarnBefore is left until the term ends
	EventNodeDown           = "node_down"            // three failed availability checks in a row on the active IP
	EventNodeUp             = "node_up"              // checks pass again after node_down
)

// Warning thresholds.
const (
	TrafficWarnPercent = 80
	ExpiryWarnBefore   = 3 * 24 * time.Hour
)

// Event is a panel event. It is sent to webhooks (Service.Notify) as JSON.
type Event struct {
	Type string    `json:"event"`
	Time time.Time `json:"time"`
	User *UserView `json:"user,omitempty"`
	Node *NodeView `json:"node,omitempty"`
}

func (s *Service) emit(e Event) {
	if s.Notify == nil {
		return
	}
	e.Time = s.now()
	s.Notify(e)
}

func (s *Service) emitUser(typ string, v UserView) { s.emit(Event{Type: typ, User: &v}) }
