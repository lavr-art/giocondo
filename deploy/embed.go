// Package deploy embeds agent installation files into the panel so it can serve them
// in an archive together with the node certificates (admin.Service.AgentBundle).
package deploy

import "embed"

// Files holds install-agent.sh. Units and the file layout on the node live in the deb packages (.goreleaser.yaml).
//
//go:embed install-agent.sh
var Files embed.FS
