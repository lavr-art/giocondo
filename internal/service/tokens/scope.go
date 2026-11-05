package tokens

// API token scopes. Each one includes the previous ones: admin ⊃ node-sync ⊃ monitor.
const (
	ScopeMonitor  = "monitor"   // read only
	ScopeNodeSync = "node-sync" // read + nodes, host overrides, agent certificates
	ScopeAdmin    = "admin"     // everything, including users and tokens
)

var scopeRank = map[string]int{ScopeMonitor: 1, ScopeNodeSync: 2, ScopeAdmin: 3}

// ScopeAllows reports whether scope have is enough for an operation that requires need.
func ScopeAllows(have, need string) bool {
	return scopeRank[have] > 0 && scopeRank[have] >= scopeRank[need]
}
