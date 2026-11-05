package panelsync

import (
	"time"

	"github.com/artemlavr/giocondo/internal/model/nodesync"
	"github.com/artemlavr/giocondo/internal/worker/enginequeue"
)

// HostMetrics is the node load reported in health messages.
type HostMetrics struct {
	Load1                                           float64
	MemTotalBytes, MemAvailableBytes, AgentRSSBytes int64
}

// hello is the first message of a session. The legacy single-engine fields describe sing-box,
// for panels that predate per-engine states.
func (s *session) hello() *nodesync.Hello {
	states := engineStates(s.syncer.Engines)
	legacy := legacyEngineState(states)

	return &nodesync.Hello{
		AgentVersion:   s.syncer.Version,
		SingboxVersion: legacy.Version,
		ConfigHash:     legacy.AppliedHash,
		InstanceID:     s.inst.id,
		Capabilities:   []string{nodesync.EngineRuntimeCapability},
		Engines:        states,
	}
}

func (s *session) health() *nodesync.AgentMessage {
	states := engineStates(s.syncer.Engines)
	legacy := legacyEngineState(states)

	var host HostMetrics
	if s.syncer.ReadHost != nil {
		host = s.syncer.ReadHost()
	}

	return &nodesync.AgentMessage{Health: &nodesync.Health{
		UptimeSeconds:     int64(time.Since(s.inst.started).Seconds()),
		Load1:             host.Load1,
		MemTotalBytes:     host.MemTotalBytes,
		MemAvailableBytes: host.MemAvailableBytes,
		AgentRssBytes:     host.AgentRSSBytes,
		SingboxActive:     legacy.Active,
		ConfigHash:        legacy.AppliedHash,
		Engines:           states,
	}}
}

func engineStates(m *enginequeue.Manager) []*nodesync.EngineState {
	states := m.States()
	out := make([]*nodesync.EngineState, 0, len(states))

	for _, st := range states {
		out = append(out, &nodesync.EngineState{
			ID: st.ID, Version: st.Version, Active: st.Active, Phase: st.Phase,
			AppliedHash: st.AppliedHash, RejectedHash: st.RejectedHash, Error: st.Error,
		})
	}

	return out
}

func legacyEngineState(states []*nodesync.EngineState) *nodesync.EngineState {
	for _, state := range states {
		if state.ID == nodesync.SingBoxEngineID {
			return state
		}
	}

	return &nodesync.EngineState{}
}
