//go:build e2e

package networke2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestCadence(t *testing.T) {
	b := newBench(t)
	b.require(os.Getenv("PANEL_HEALTH_INTERVAL") == "5m", "cadence requires PANEL_HEALTH_INTERVAL=5m")
	nodes := list(b.mustAPI("GET", "nodes", nil))
	b.equal(len(nodes), 1, "cadence node count")
	n := obj(nodes[0])
	b.nodeID = b.intField(n, "id")
	b.equal(n["ip"], "172.30.77.21", "post-network node IP")
	for _, value := range list(n["addresses"]) {
		a := obj(value)
		if a["ip"] == "172.30.77.99" {
			b.mustAPI("DELETE", b.nodePath(fmt.Sprintf("/addresses/%d", b.intField(a, "id"))), nil)
		}
	}
	b.wait("healthy active node before cadence check", 3*time.Minute, func() (bool, error) {
		current, e := b.node()
		if e != nil {
			return false, e
		}
		probes := b.probesFor(current, b.stringField(n, "ip"))
		return len(probes) == 6 && b.noProbeErrors(probes), nil
	})
	b.drop("node2", "-d", str(n["ip"]), "-p", "tcp", "--dport", "443", "-j", "DROP")
	b.checked(b.compose("up", "-d", "--force-recreate", "panel"))
	observations := []object{}
	parent := b.ctx
	ctx, cancel := context.WithTimeout(parent, 780*time.Second)
	b.ctx = ctx
	defer func() { b.ctx = parent; cancel() }()
	deadline, _ := ctx.Deadline()
	var lastErr error
	finished := false
	for time.Now().Before(deadline) {
		current, e := b.node()
		if e != nil {
			lastErr = e
		} else {
			var probe object
			for _, p := range b.probesFor(current, str(n["ip"])) {
				if p["tag"] == "vless" && p["point"] == "panel" {
					probe = p
					break
				}
			}
			if probe != nil && b.intField(probe, "failures") > 0 && (len(observations) == 0 || observations[len(observations)-1]["failures"] != probe["failures"]) {
				observations = append(observations, probe)
				t.Log("CADENCE", probe["failures"], probe["checked_at"])
				if b.intField(probe, "failures") < 3 {
					b.require(!b.boolField(current, "warning"), "early warning")
				}
			}
			if b.boolField(current, "warning") && len(observations) > 0 && b.intField(observations[len(observations)-1], "failures") == 3 {
				finished = true
				break
			}
		}
		if e := b.pause(5 * time.Second); e != nil {
			break
		}
	}
	b.require(finished, "5-minute cadence test timed out; last error: %v", lastErr)
	failures := []int{}
	stamps := []time.Time{}
	for _, p := range observations {
		failures = append(failures, b.intField(p, "failures"))
		stamp, e := time.Parse(time.RFC3339Nano, str(p["checked_at"]))
		if e != nil {
			t.Fatal(e)
		}
		stamps = append(stamps, stamp)
	}
	b.equal(failures, []int{1, 2, 3}, "cadence failure progression")
	intervals := []float64{}
	for i := 1; i < len(stamps); i++ {
		delta := stamps[i].Sub(stamps[i-1]).Seconds()
		intervals = append(intervals, delta)
		b.require(delta >= 285 && delta <= 315, "production check interval is %.1fs, expected 285–315s", delta)
	}
	b.pass("production 5-minute intervals")
	t.Log("intervals:", intervals)
	b.writeJSON("cadence-results.json", object{"intervals_seconds": intervals, "observations": observations})
}
