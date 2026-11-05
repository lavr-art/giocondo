package operation

import "sync"

// NodeStateLock serializes operations that read a node's stored state, decide and write it back: node
// updates and deletion, network changes (DNS, addresses, IP rotation) and health results. The services
// that make them share one lock owned by the application; the zero value is ready to use.
type NodeStateLock struct{ sync.Mutex }
