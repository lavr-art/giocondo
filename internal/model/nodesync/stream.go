package nodesync

// Stream is one Sync stream as seen by the agent. Send and Recv may run concurrently with each
// other, but each must be called from one goroutine at a time.
type Stream interface {
	Send(*AgentMessage) error
	Recv() (*PanelMessage, error)
	CloseSend() error
}
