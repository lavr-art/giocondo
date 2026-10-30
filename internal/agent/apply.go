package agent

import "github.com/artemlavr/giocondo/internal/agent/engine"

// Hash is shared by the panel's legacy sing-box configuration delivery.
func Hash(data []byte) string { return engine.Hash(data) }
