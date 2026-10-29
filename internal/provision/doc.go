// Package provision installs the agent on a node: over SSH (Install) or via cloud-init user-data
// (CloudInit). Cloud APIs (server creation) are not implemented. Panel only: the agent must not
// import this package (cmd/agent/deps_test.go).
package provision
