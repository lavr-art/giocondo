//go:build e2e

package networke2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

const installerIP = "172.30.77.31"
const installerCommand = "curl -fsSL http://control:8000/repo/install.sh -o /root/install.sh && sh /root/install.sh --ip " + installerIP + " --http --repo http://control:8000/repo --yes"

var stateFiles = []string{"/etc/giocondo-vpn/panel.env", "/etc/giocondo-vpn/agent/agent.crt", "/var/lib/giocondo-vpn/panel/pki/server.crt"}
var services = []string{"giocondo-vpn-panel", "giocondo-vpn-agent", "giocondo-vpn-sing-box"}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func (b *bench) installerAPI(method, path string, body any) (any, error) {
	data := ""
	if body != nil {
		value, e := json.Marshal(body)
		if e != nil {
			return nil, e
		}
		data = " --data-binary " + shellQuote(string(value))
	}
	command := ". /etc/giocondo-vpn/panel.env && curl -fsS --max-time 25 -X " + shellQuote(method) + " -H \"Authorization: Bearer $(cat /root/token)\" -H 'Content-Type: application/json'" + data + " \"http://127.0.0.1:8080${PANEL_ADMIN_PATH}/api/" + path + "\""
	return decode(b.shell(command))
}
func (b *bench) mustInstallerAPI(method, path string, body any) any {
	b.t.Helper()
	v, e := b.installerAPI(method, path, body)
	if e != nil {
		b.t.Fatal(e)
	}
	return v
}
func (b *bench) snapshot() string {
	return b.checked(b.shell("sha256sum "+strings.Join(stateFiles, " "))) + b.checked(b.shell("systemctl show -p ActiveEnterTimestamp "+strings.Join(services, " ")))
}
func (b *bench) installerNodeApplied() (bool, error) {
	v, e := b.installerAPI("GET", "nodes", nil)
	if e != nil {
		return false, e
	}
	nodes := list(v)
	if len(nodes) != 1 {
		return false, nil
	}
	n := obj(nodes[0])
	return n["status"] == "online" && !b.boolField(n, "dirty") && b.stringField(n, "apply_error") == "", nil
}
func (b *bench) installerTraffic(port int) bool {
	return b.traffic(port, "https://192.0.2.200:8443/installer-challenge.txt", strings.Repeat("b", 64))
}
func TestInstaller(t *testing.T) { b := newBench(t); b.installer(nil) }
func (b *bench) installer(exercise func(object, map[string]object)) {
	b.write("installer-challenge.txt", []byte(strings.Repeat("b", 64)))
	b.wait("systemd ready in the clean server", 3*time.Minute, func() (bool, error) {
		r := b.shell("systemctl is-system-running --wait")
		return r.err == nil || strings.TrimSpace(r.stdout) == "degraded", nil
	})
	out := b.checked(b.shell(b.install))
	b.require(strings.Contains(out, "giocondo-vpn is installed."), "installer did not report success: %s", out)
	b.wait("all services active after install", 2*time.Minute, func() (bool, error) {
		for _, s := range services {
			r := b.shell("systemctl is-active " + s)
			if strings.TrimSpace(r.stdout) != "active" {
				return false, commandError(r)
			}
		}
		return true, nil
	})
	b.checked(b.shell("runuser -u giocondo-vpn -- giocondo-vpn-panel token create -name e2e -scope admin -db /var/lib/giocondo-vpn/panel/panel.db | tail -n 1 > /root/token && chmod 600 /root/token"))
	b.wait("local node online with the config applied", 3*time.Minute, b.installerNodeApplied)
	user := obj(b.mustInstallerAPI("POST", "users", object{"name": "bob", "days": 30}))
	url := str(user["subscription_url"])
	token := url[strings.LastIndex(url, "/")+1:]
	b.equal(url, "http://"+installerIP+":8081/sub/"+token, "public subscription URL")
	var profile object
	b.wait("subscription served on the public IP", time.Minute, func() (bool, error) {
		var e error
		profile, e = b.profile("http://" + installerIP + ":8081/sub/" + token + "?format=singbox")
		return profile != nil, e
	})
	selected := map[string]object{}
	for _, v := range list(profile["outbounds"]) {
		out := obj(v)
		kind := str(out["type"])
		if kind == "vless" || kind == "hysteria2" {
			selected[kind] = out
		}
	}
	endpoints := list(profile["endpoints"])
	b.require(len(endpoints) > 0, "installer subscription has no WireGuard endpoint")
	selected["wireguard"] = obj(endpoints[0])
	b.equal(sortedKeys(selected), []string{"hysteria2", "vless", "wireguard"}, "installer protocols")
	for i, kind := range sortedKeys(selected) {
		port := 19101 + i
		b.startClient("installer-"+kind, port, selected[kind], kind == "wireguard")
		b.wait("client traffic through the installed node: "+kind, 90*time.Second, func() (bool, error) { return b.installerTraffic(port), nil })
	}
	fingerprints := b.fingerprints()
	if exercise != nil {
		exercise(user, selected)
	}
	before := b.snapshot()
	out = b.checked(b.shell(b.install))
	b.require(strings.Contains(out, "giocondo-vpn is installed."), "second installer run did not report success: %s", out)
	b.equal(b.snapshot(), before, "second installer run state and service timestamps")
	b.pass("installer is idempotent")
	healthyFiles := b.checked(b.shell("sha256sum " + strings.Join(stateFiles, " ")))
	otherServices := b.checked(b.shell("systemctl show -p ActiveEnterTimestamp giocondo-vpn-panel giocondo-vpn-sing-box"))
	b.checked(b.shell("systemctl stop giocondo-vpn-agent"))
	out = b.checked(b.shell(b.install))
	b.require(strings.Contains(out, "giocondo-vpn is installed."), "recovery installer did not report success: %s", out)
	b.equal(b.checked(b.shell("sha256sum "+strings.Join(stateFiles, " "))), healthyFiles, "recovery credentials")
	b.equal(b.checked(b.shell("systemctl show -p ActiveEnterTimestamp giocondo-vpn-panel giocondo-vpn-sing-box")), otherServices, "healthy service timestamps after recovery")
	applied, e := b.installerNodeApplied()
	if e != nil {
		b.t.Fatal(e)
	}
	b.require(applied, "restarted agent is not ready")
	b.pass("stopped agent recovered safely")
	status := b.checked(b.shell("sh /root/install.sh status"))
	states := map[string]string{}
	for line := range strings.SplitSeq(status, "\n") {
		if strings.HasPrefix(line, "giocondo-vpn-") {
			fields := strings.Fields(line)
			b.require(len(fields) >= 2, "malformed status line: %s", line)
			states[fields[0]] = fields[1]
		}
	}
	b.equal(states, map[string]string{"giocondo-vpn-panel": "active", "giocondo-vpn-agent": "active", "giocondo-vpn-sing-box": "active"}, "installer status services")
	b.pass("status reports active services")
	b.checked(b.shell("apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq caddy >/dev/null"))
	b.checked(b.execute("installer", "/work/bench", "caddy-check"))
	b.pass("generated domain and ip Caddy HTTPS proxies with verified local TLS")
	b.equal(b.fingerprints(), fingerprints, "unchanged installer client configurations")
	names := []string{}
	for _, kind := range sortedKeys(selected) {
		names = append(names, "installer-"+kind)
	}
	b.liveClients(names)
	b.reportFile("installer-results.txt")
}
func (b *bench) upgradeTraffic(selected map[string]object) {
	for i, kind := range sortedKeys(selected) {
		port := 19101 + i
		b.wait("unchanged client after upgrade: "+kind, 2*time.Minute, func() (bool, error) { return b.installerTraffic(port), nil })
	}
}
func (b *bench) identity() string {
	out := b.checked(b.shell("find /etc/giocondo-vpn/agent /var/lib/giocondo-vpn/panel/pki -type f -exec sha256sum {} +; sha256sum /etc/giocondo-vpn/panel.env /etc/giocondo-vpn/agent.env /etc/giocondo-vpn/sing-box/config.json"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	sortStrings(lines)
	return strings.Join(lines, "\n")
}
func packageVersionCommand(name string) string {
	return fmt.Sprintf("dpkg-query -W -f='${Version}' %s", name)
}
