// Package apt embeds the agent installer into the panel so it can serve it in an archive
// together with the node certificates (admin.Service.AgentBundle).
package apt

import "embed"

// Files holds install-agent.sh. Units and the file layout on the node live in the deb packages (.goreleaser.yaml).
//
//go:embed install-agent.sh
var Files embed.FS
