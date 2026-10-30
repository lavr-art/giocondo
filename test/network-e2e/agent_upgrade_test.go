//go:build e2e

package networke2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAgentUpgrade(t *testing.T) {
	b := newBench(t)
	_, e := os.Stat(filepath.Join(b.work, "agent-legacy"))
	if e != nil {
		t.Fatal("Build the previous Linux agent into bin/network-e2e/agent-legacy first: ", e)
	}
	nodes := list(b.mustAPI("GET", "nodes", nil))
	b.require(len(nodes) > 0, "network test node missing")
	active := obj(nodes[0])
	b.nodeID = b.intField(active, "id")
	service := map[string]string{"172.30.77.11": "node1", "172.30.77.12": "node1", "172.30.77.21": "node2"}[str(active["ip"])]
	b.require(service != "", "unknown active bench node")
	container := strings.TrimSpace(b.checked(b.compose("ps", "-q", service)))
	b.require(container != "", "active node container missing")
	b.checked(b.command(t.Context(), time.Minute, "", "docker", "update", "--cpus", "1", "--memory", "1g", "--memory-swap", "1g", container))
	b.checked(b.compose("cp", filepath.Join(b.work, "agent-legacy"), service+":/tmp/giocondo-agent-legacy"))
	b.checked(b.compose("cp", filepath.Join(b.work, "agent"), service+":/tmp/giocondo-agent-current"))
	execute := func(args ...string) string { return b.checked(b.execute(service, args...)) }
	stop := func() {
		execute("systemctl", "stop", "giocondo-vpn-agent")
		b.wait("agent offline before binary replacement", 30*time.Second, func() (bool, error) {
			n, e := b.node()
			if e != nil {
				return false, e
			}
			return !b.boolField(n, "agent_connected"), nil
		})
	}
	connected := func() (bool, error) {
		n, e := b.node()
		if e != nil {
			return false, e
		}
		return b.boolField(n, "agent_connected"), nil
	}
	stop()
	// Keep historical journals; a pre-runtime agent has no engine journal.
	execute("sh", "-c", "p=/var/lib/giocondo-vpn/agent/sing-box; if [ -e \"$p\" ]; then mv \"$p\" \"$p-before-upgrade-$(date +%s%N)\"; fi")
	execute("install", "-m", "0755", "/tmp/giocondo-agent-legacy", "/usr/bin/giocondo-vpn-agent")
	execute("systemctl", "start", "giocondo-vpn-agent")
	b.wait("legacy agent connected", 30*time.Second, connected)
	b.wait("legacy agent WG traffic", 30*time.Second, func() (bool, error) { return b.clientTraffic("wg"), nil })
	if e := b.pause(3 * time.Second); e != nil {
		t.Fatal(e)
	}
	config := execute("cat", "/etc/giocondo-vpn/sing-box/config.json")
	pid := strings.TrimSpace(execute("systemctl", "show", "-p", "MainPID", "--value", "giocondo-vpn-sing-box"))
	started := execute("systemctl", "show", "-p", "ExecMainStartTimestampMonotonic", "--value", "giocondo-vpn-sing-box")
	fingerprints := b.fingerprints()
	stop()
	execute("install", "-m", "0755", "/tmp/giocondo-agent-current", "/usr/bin/giocondo-vpn-agent")
	execute("systemctl", "start", "giocondo-vpn-agent")
	b.wait("upgraded agent connected", 30*time.Second, connected)
	for _, kind := range []string{"vless", "hy2", "wg"} {
		b.wait("unchanged client after agent upgrade "+kind, 30*time.Second, func() (bool, error) { return b.clientTraffic(kind), nil })
	}
	b.equal(execute("cat", "/etc/giocondo-vpn/sing-box/config.json"), config, "agent-upgrade core configuration")
	b.equal(strings.TrimSpace(execute("systemctl", "show", "-p", "MainPID", "--value", "giocondo-vpn-sing-box")), pid, "core PID after agent upgrade")
	b.equal(execute("systemctl", "show", "-p", "ExecMainStartTimestampMonotonic", "--value", "giocondo-vpn-sing-box"), started, "core process start timestamp")
	b.equal(b.fingerprints(), fingerprints, "agent-upgrade client configurations")
	b.liveClients([]string{"vless", "hy2", "wg"})
	b.pass("agent upgrade preserved config, client keys and sing-box process")
	agentPID := strings.TrimSpace(execute("systemctl", "show", "-p", "MainPID", "--value", "giocondo-vpn-agent"))
	pidNumber, e := strconv.Atoi(agentPID)
	if e != nil || pidNumber <= 0 {
		t.Fatalf("invalid agent PID: %q", agentPID)
	}
	rss := []int64{}
	for range 5 {
		status := execute("cat", fmt.Sprintf("/proc/%d/status", pidNumber))
		found := false
		for line := range strings.SplitSeq(status, "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				fields := strings.Fields(line)
				b.require(len(fields) >= 2, "malformed VmRSS")
				value, e := strconv.ParseInt(fields[1], 10, 64)
				if e != nil {
					t.Fatal(e)
				}
				rss = append(rss, value*1024)
				found = true
				break
			}
		}
		b.require(found, "agent process status has no RSS")
		if e := b.pause(2 * time.Second); e != nil {
			t.Fatal(e)
		}
	}
	limit, e := strconv.ParseInt(strings.TrimSpace(execute("systemctl", "show", "-p", "MemoryMax", "--value", "giocondo-vpn-agent")), 10, 64)
	if e != nil {
		t.Fatal(e)
	}
	b.equal(limit, int64(64*1024*1024), "agent service memory limit")
	b.writeJSON("agent-upgrade-results.json", object{"checks": b.report, "agent_rss_bytes": rss, "service_memory_max_bytes": limit, "node_cpus": 1, "node_memory_bytes": 1024 * 1024 * 1024})
	t.Log("PASS agent upgrade without sing-box restart; RSS bytes:", rss)
}
