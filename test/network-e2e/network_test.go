//go:build e2e

package networke2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNetwork(t *testing.T) {
	b := newBench(t)
	b.wait("panel API", 3*time.Minute, func() (bool, error) { v, e := b.api("GET", "nodes", nil); return v != nil, e })
	existing := list(b.mustAPI("GET", "nodes", nil))
	var user, preview object
	if len(existing) > 0 {
		b.require(os.Getenv("NETWORK_E2E_RESUME") == "1", "Use a fresh test DB/work directory; prior reports are preserved")
		b.nodeID = b.intField(obj(existing[0]), "id")
		users := list(b.mustAPI("GET", "users", nil))
		b.require(len(users) > 0, "resumed bench has no user")
		user = obj(users[0])
		preview = obj(b.mustAPI("GET", b.nodePath("/dns"), nil))
	} else {
		created := obj(b.mustAPI("POST", "nodes", object{"name": "n1", "host": "172.30.77.11", "ip": "172.30.77.11", "vless_reality": object{"dest": "192.0.2.200:8443", "server_name": "control"}, "hysteria2": object{"obfs": true}, "wireguard": object{}}))
		b.nodeID = b.intField(created, "id")
		user = obj(b.mustAPI("POST", "users", object{"name": "alice", "days": 30}))
		b.mustAPI("POST", b.nodePath("/addresses"), object{"ip": "172.30.77.12"})
		preview = obj(b.mustAPI("GET", b.nodePath("/dns"), nil))
		b.equal(preview["host"], fmt.Sprintf("n%d.vpn.test", b.nodeID), "DNS host")
		b.mustAPI("POST", b.nodePath("/dns"), object{"confirm": true})
	}
	b.job(b.mustAPI("POST", b.nodePath("/provision-ssh"), object{"host": "172.30.77.11", "password": "network-test-only", "keep_key": true}))
	b.wait("all three protocols healthy from panel and external probe", 7*time.Minute, func() (bool, error) {
		n, e := b.node()
		if e != nil {
			return false, e
		}
		probes := b.probesFor(n, str(n["ip"]))
		return n["status"] == "online" && !b.boolField(n, "dirty") && len(probes) >= 6 && b.noProbeErrors(probes), nil
	})
	b.wait("reserve IP supports UDP from both points", 7*time.Minute, func() (bool, error) {
		n, e := b.node()
		if e != nil {
			return false, e
		}
		count := 0
		for _, p := range b.probesFor(n, "172.30.77.12") {
			if b.stringField(p, "error") == "" {
				count++
			}
		}
		return count == 6, nil
	})
	subscription := str(user["subscription_url"])
	token := subscription[strings.LastIndex(subscription, "/")+1:]
	profile, e := b.profile("http://panel:8081/sub/" + token + "?format=singbox")
	if e != nil {
		t.Fatal(e)
	}
	selected := map[string]object{}
	for _, v := range list(profile["outbounds"]) {
		out := obj(v)
		if out["type"] == "vless" {
			selected["vless"] = out
		}
		if out["type"] == "hysteria2" {
			selected["hy2"] = out
		}
	}
	endpoints := list(profile["endpoints"])
	b.require(len(endpoints) > 0, "subscription has no WireGuard endpoint")
	selected["wg"] = obj(endpoints[0])
	b.require(len(selected) == 3, "subscription must contain all three protocols")
	kinds := []string{"vless", "hy2", "wg"}
	for i, kind := range kinds {
		out := selected[kind]
		host := str(out["server"])
		if host == "" {
			peers := list(out["peers"])
			if len(peers) > 0 {
				host = str(obj(peers[0])["address"])
			}
		}
		b.equal(host, str(preview["host"]), "client DNS host")
		b.startClient(kind, 19001+i, out, kind == "wg")
	}
	fingerprints := b.fingerprints()
	for _, kind := range kinds {
		b.wait("initial client traffic "+kind, time.Minute, func() (bool, error) { return b.clientTraffic(kind), nil })
	}
	before := b.checked(b.execute("node1", "cat", "/etc/giocondo-vpn/sing-box/config.json"))
	b.drop("node1", "-d", "172.30.77.11", "-p", "tcp", "-m", "multiport", "--dports", "443", "-j", "DROP")
	b.drop("node1", "-d", "172.30.77.11", "-p", "udp", "-m", "multiport", "--dports", "8443,51820", "-j", "DROP")
	timeout := 5 * time.Minute
	if os.Getenv("PANEL_HEALTH_INTERVAL") == "5m" {
		timeout = 20 * time.Minute
	}
	b.wait("warning after three failures with live agent", timeout, func() (bool, error) {
		n, e := b.node()
		if e != nil {
			return false, e
		}
		probes := b.probesFor(n, "172.30.77.11")
		if !b.boolField(n, "warning") || !b.boolField(n, "agent_connected") || len(probes) != 6 {
			return false, nil
		}
		for _, p := range probes {
			if b.intField(p, "failures") != 3 {
				return false, nil
			}
		}
		return true, nil
	})
	for _, p := range b.probesFor(b.mustNode(), "172.30.77.11") {
		b.equal(b.intField(p, "failures"), 3, "blocked probe failures")
	}
	b.requireBlockedClient("hy2")
	b.requireBlockedClient("wg")
	b.liveClients(kinds)
	started := time.Now()
	b.job(b.mustAPI("POST", b.nodePath("/rotate-ip"), object{"mode": "existing_ip", "ip": "172.30.77.12"}))
	for _, kind := range kinds {
		b.wait("same client process reconnects after DNS switch "+kind, 150*time.Second, func() (bool, error) { return b.clientTraffic(kind), nil })
	}
	b.report = append(b.report, fmt.Sprintf("DNS reconnection elapsed %.1fs (TTL 60)", time.Since(started).Seconds()))
	b.equal(b.checked(b.execute("node1", "cat", "/etc/giocondo-vpn/sing-box/config.json")), before, "config after DNS switch")
	b.wait("warning cleared on new IP", 3*time.Minute, func() (bool, error) {
		n, e := b.node()
		if e != nil {
			return false, e
		}
		return !b.boolField(n, "warning"), nil
	})
	b.equal(b.fingerprints(), fingerprints, "client configs after DNS switch")
	b.liveClients(kinds)
	b.job(b.mustAPI("POST", b.nodePath("/rotate-ip"), object{"mode": "ssh", "ip": "172.30.77.21", "ssh": object{"password": "network-test-only", "keep_key": true}, "source_ssh": object{"host": "172.30.77.12", "password": "network-test-only"}}))
	for _, kind := range kinds {
		b.wait("same client config after SSH migration "+kind, 150*time.Second, func() (bool, error) { return b.clientTraffic(kind), nil })
	}
	b.equal(b.checked(b.execute("node2", "cat", "/etc/giocondo-vpn/sing-box/config.json")), before, "migrated complete config")
	n := b.mustNode()
	b.equal(n["host"], preview["host"], "migrated host")
	b.equal(n["ip"], "172.30.77.21", "migrated IP")
	stopped, status, err := b.executeStatus("node1", "systemctl", "is-active", "giocondo-vpn-agent")
	if err != nil {
		t.Fatal("source-agent status check failed: ", err)
	}
	b.require(status == 3 && strings.TrimSpace(stopped.stdout) == "inactive", "old agent is not inactive (exit %d): %s", status, stopped.stdout)
	b.equal(b.fingerprints(), fingerprints, "client configs after SSH migration")
	b.liveClients(kinds)
	b.pass("SSH migration preserved complete config, keys, node ID and subscription")
	b.mustAPI("GET", b.nodePath("/addresses"), nil)
	b.mustAPI("POST", b.nodePath("/addresses"), object{"ip": "172.30.77.99"})
	failed := obj(b.mustAPI("POST", b.nodePath("/rotate-ip"), object{"mode": "existing_ip", "ip": "172.30.77.99"}))
	b.wait("unreachable candidate fails without DNS switch", 90*time.Second, func() (bool, error) {
		v, e := b.api("GET", "jobs/"+str(failed["id"]), nil)
		if e != nil {
			return false, e
		}
		status := obj(v)["status"]
		if status == "ok" {
			return false, terminalError{"unreachable candidate unexpectedly succeeded"}
		}
		return status == "failed", nil
	})
	b.equal(b.mustNode()["ip"], "172.30.77.21", "active IP after candidate failure")
	b.pass("unreachable candidate leaves active IP unchanged")
	b.reportFile("results.txt")
}
func (b *bench) probesFor(n object, ip string) []object {
	var result []object
	for _, v := range list(n["probes"]) {
		p := obj(v)
		if p["ip"] == ip {
			result = append(result, p)
		}
	}
	return result
}
func (b *bench) noProbeErrors(probes []object) bool {
	for _, p := range probes {
		if b.stringField(p, "error") != "" {
			return false
		}
	}
	return true
}
