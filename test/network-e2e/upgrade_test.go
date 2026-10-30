//go:build e2e

package networke2e

import (
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

func sortStrings(values []string) { sort.Strings(values) }
func releaseBranch(version string) string {
	parts := strings.Split(strings.SplitN(version, "~", 2)[0], ".")
	if parts[0] == "0" && len(parts) > 1 {
		return strings.Join(parts[:2], ".")
	}
	return parts[0]
}
func TestUpgrade(t *testing.T) {
	b := newBench(t)
	baseline := strings.TrimSpace(b.file("baseline-version"))
	candidate := strings.TrimSpace(b.file("candidate-version"))
	b.require(baseline != candidate, "upgrade test needs different releases")
	candidateInstall := b.install
	b.install = strings.ReplaceAll(candidateInstall, "/repo", "/baseline-repo")
	breaking := releaseBranch(baseline) != releaseBranch(candidate)
	allowedInstall := candidateInstall
	allowAPT := ""
	if breaking {
		allowedInstall += " --allow-breaking-upgrade"
		allowAPT = "GIOCONDO_ALLOW_BREAKING_UPGRADE=1 "
	}
	b.installer(func(_ object, selected map[string]object) {
		before := b.identity()
		users := list(b.mustInstallerAPI("GET", "users", nil))
		b.require(len(users) > 0, "baseline user missing")
		initialUser := obj(users[0])
		fingerprints := b.fingerprints()
		b.checked(b.shell("sed -i 's|/baseline-repo|/repo|g' /etc/apt/sources.list.d/giocondo-vpn.list; apt-get update -qq"))
		target := shellQuote(candidate)
		if breaking {
			refused := b.shell("DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends giocondo-vpn-panel=" + target)
			b.require(refused.err != nil, "breaking upgrade succeeded without opt-in")
			b.equal(b.identity(), before, "identity after refused transition")
			b.equal(b.checked(b.shell(packageVersionCommand("giocondo-vpn-panel"))), baseline, "panel version after refused transition")
		}
		b.checked(b.shell(allowAPT + "DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends giocondo-vpn-panel=" + target))
		b.equal(b.checked(b.shell(packageVersionCommand("giocondo-vpn-agent"))), baseline, "mixed-version old agent")
		b.checked(b.shell("cp /usr/share/giocondo-vpn/panel/recover.sh /root/giocondo-recover.sh"))
		b.wait("new panel accepts previous stable agent", 3*time.Minute, func() (bool, error) {
			v, e := b.installerAPI("GET", "nodes", nil)
			nodes := list(v)
			return len(nodes) > 0 && obj(nodes[0])["status"] == "online", e
		})
		b.upgradeTraffic(selected)
		snapshot := strings.TrimSpace(b.checked(b.shell("find /var/lib/giocondo-vpn/backups -mindepth 1 -maxdepth 1 -type d")))
		b.require(snapshot != "" && !strings.Contains(snapshot, "\n"), "expected one coherent baseline snapshot: %s", snapshot)
		if os.Getenv("UPGRADE_PATH") == "apt" {
			packages := []string{}
			for _, p := range services {
				packages = append(packages, p+"="+target)
			}
			b.checked(b.shell(allowAPT + "DEBIAN_FRONTEND=noninteractive apt-get install -y " + strings.Join(packages, " ")))
		} else {
			out := b.checked(b.shell(allowedInstall))
			b.require(strings.Contains(out, "giocondo-vpn is installed."), "upgrade installer did not report success: %s", out)
		}
		b.install = allowedInstall
		b.upgradeTraffic(selected)
		b.equal(b.identity(), before, "identity after upgrade")
		current := list(b.mustInstallerAPI("GET", "users", nil))
		b.require(len(current) > 0, "upgraded user missing")
		for _, key := range []string{"id", "name", "uuid", "subscription_url", "expires_at", "traffic_limit_bytes"} {
			b.equal(obj(current[0])[key], initialUser[key], "upgraded user "+key)
		}
		b.equal(b.fingerprints(), fingerprints, "upgrade client configurations")
		b.pass("previous stable upgraded with preserved identity and all three client protocols")
		recovery := "/root/giocondo-recover.sh"
		b.checked(b.shell("sh " + recovery + " verify " + shellQuote(snapshot)))
		b.checked(b.shell("systemctl stop giocondo-vpn-agent giocondo-vpn-panel giocondo-vpn-sing-box"))
		debs := strings.Fields(b.checked(b.shell("find /work/baseline -name '*.deb'")))
		b.require(len(debs) > 0, "baseline deb packages missing")
		for i, p := range debs {
			debs[i] = shellQuote(p)
		}
		b.checked(b.shell("GIOCONDO_RESTORE_FROM=" + shellQuote(snapshot) + " dpkg --unpack " + strings.Join(debs, " ")))
		b.checked(b.shell("sh " + recovery + " restore-state " + shellQuote(snapshot)))
		b.checked(b.shell("dpkg --configure -a"))
		b.checked(b.shell("systemctl daemon-reload; while IFS= read -r service; do systemctl start \"$service\"; done < " + shellQuote(snapshot+"/active-services")))
		b.upgradeTraffic(selected)
		b.equal(b.identity(), before, "identity after manual recovery")
		b.equal(b.fingerprints(), fingerprints, "recovery client configurations")
		b.pass("manual package and state recovery restores unchanged client traffic")
		b.checked(b.shell(allowedInstall))
		b.upgradeTraffic(selected)
	})
	b.reportFile("upgrade-results.txt")
}
