package installer

import (
	"crypto/ed25519"
	"crypto/md5"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type installerFixture struct {
	t                         *testing.T
	base, bin, env, caddy, ca string
}

func newInstaller(t *testing.T) *installerFixture {
	t.Helper()
	base := t.TempDir()
	f := &installerFixture{t: t, base: base, bin: filepath.Join(base, "bin"), env: filepath.Join(base, "panel.env"), caddy: filepath.Join(base, "Caddyfile")}
	mkdir(t, f.bin)
	mkdir(t, filepath.Join(base, "pki"))
	f.stub("caddy", "exit 0")
	f.stub("systemctl", "exit 0")
	f.stub("dpkg-query", "exit 1")
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Installer test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	f.ca = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	writeFile(t, filepath.Join(base, "ca-source.crt"), f.ca)
	return f
}
func (f *installerFixture) stub(name, body string) { f.t.Helper(); stub(f.t, f.bin, name, body) }
func (f *installerFixture) shell(body string, success bool, realCaddy ...bool) commandResult {
	f.t.Helper()
	source := readFile(f.t, filepath.Join(repositoryRoot(f.t), "install.sh"))
	index := strings.LastIndex(source, `main "$@"`)
	if index < 0 {
		f.t.Fatal("installer main entry point missing")
	}
	source = source[:index]
	prefix := fmt.Sprintf("\nENV_FILE=%s\nCADDYFILE=%s\nINSTALL_STATE=%s\nCA_BUNDLE=%s\nPKI=%s\nAGENT_CERT=%s\nDOMAIN=vpn.example.com IP=192.0.2.1 MODE=domain RECONFIGURE=0 EXISTING=1 PANEL_CHANGED=0 YES=1 HTTP_SET=0\n", quote(f.env), quote(f.caddy), quote(filepath.Join(f.base, "installer")), quote(filepath.Join(f.base, "ca.crt")), quote(filepath.Join(f.base, "pki")), quote(filepath.Join(f.base, "agent.crt")))
	if len(realCaddy) == 0 || !realCaddy[0] {
		prefix += "install_caddy() { :; }\n"
	}
	r := runCommand(f.t, []string{"PATH=" + f.bin + string(os.PathListSeparator) + os.Getenv("PATH")}, "sh", "-eu", "-c", source+prefix+body)
	if success {
		r.requireSuccess(f.t)
	} else {
		r.requireFailure(f.t)
	}
	return r
}
func (f *installerFixture) resolve(args string) commandResult {
	f.t.Helper()
	return f.shell(`DOMAIN= IP= DOMAIN_SET=0 IP_SET=0; detect_ip() { echo 192.0.2.1; }; `+args+`; prepare_config; printf "MODE=%s DOMAIN=%s\n" "$MODE" "$DOMAIN"`, true)
}
func (f *installerFixture) rebuildCA() {
	f.t.Helper()
	f.stub("update-ca-certificates", "cp "+quote(filepath.Join(f.base, "ca-source.crt"))+" "+quote(filepath.Join(f.base, "ca.crt")))
}
func (f *installerFixture) packageDefaultCaddy() {
	f.t.Helper()
	writeFile(f.t, f.caddy, ":80 { file_server }\n")
	sum := md5.Sum([]byte(readFile(f.t, f.caddy)))
	f.stub("dpkg-query", fmt.Sprintf("printf ' %%s %%s\\n' %s %s", quote(f.caddy), quote(fmt.Sprintf("%x", sum))))
}

func TestInstallerRepeatPreservesExistingSettings(t *testing.T) {
	f := newInstaller(t)
	content := "PANEL_PUBLIC_URL=https://vpn.example.com:8443\nPANEL_SUB_URL=https://vpn.example.com:8443\nPANEL_GRPC_LISTEN=:9443\nPANEL_LISTEN=127.0.0.1:9090\n"
	writeFile(t, f.env, content)
	f.shell("DOMAIN=; configure_panel", true)
	equal(t, readFile(t, f.env), content)
}
func TestInstallerStartPanelRequiresHTTPReadinessBeforeDatabaseInspection(t *testing.T) {
	f := newInstaller(t)
	writeFile(t, f.env, "PANEL_SUB_LISTEN=0.0.0.0:8081\n")
	seen := filepath.Join(f.base, "http-checked")
	f.stub("curl", `for arg in "$@"; do url=$arg; done
printf '%s' "$url" > `+quote(seen)+`
printf '%s' "${url##*=}"`)
	f.shell("start_panel", true)
	equal(t, exists(t, seen), true)
	if !strings.HasPrefix(readFile(t, seen), "http://127.0.0.1:8081/health/challenge?challenge=") {
		t.Fatal("readiness did not check the panel HTTP challenge")
	}
}
func TestInstallerReconfigurePreservesGRPC(t *testing.T) {
	f := newInstaller(t)
	writeFile(t, f.env, "PANEL_GRPC_LISTEN=:9555\nPANEL_LISTEN=127.0.0.1:8080\n")
	f.shell("RECONFIGURE=1; configure_panel", true)
	contains(t, readFile(t, f.env), "PANEL_GRPC_LISTEN=:9555\n")
}
func TestInstallerReconfigureIPURLsUseExistingListenerPorts(t *testing.T) {
	f := newInstaller(t)
	writeFile(t, f.env, "PANEL_LISTEN=127.0.0.1:9090\nPANEL_SUB_LISTEN=127.0.0.1:9091\n")
	f.shell("DOMAIN= MODE=http RECONFIGURE=1; configure_panel", true)
	contains(t, readFile(t, f.env), "PANEL_PUBLIC_URL=http://127.0.0.1:9090\n")
	contains(t, readFile(t, f.env), "PANEL_SUB_URL=http://192.0.2.1:9091\n")
}
func TestInstallerNewInstallDefaultsToHTTPSByIP(t *testing.T) {
	f := newInstaller(t)
	writeFile(t, f.env, "PANEL_LISTEN=127.0.0.1:9090\nPANEL_SUB_LISTEN=127.0.0.1:9091\n")
	r := f.shell(`DOMAIN= IP= DOMAIN_SET=0 IP_SET=0 EXISTING=0; detect_ip() { echo 192.0.2.1; }; prepare_config; configure_panel; printf "MODE=%s\n" "$MODE"`, true)
	contains(t, r.stdout, "MODE=ip")
	content := readFile(t, f.env)
	contains(t, content, "PANEL_PUBLIC_URL=http://127.0.0.1:9090\n")
	contains(t, content, "PANEL_SUB_URL=https://192.0.2.1:8443\n")
	contains(t, content, "PANEL_SUB_LISTEN=127.0.0.1:9091\n")
}
func TestInstallerHTTPFlagKeepsPlainHTTP(t *testing.T) {
	f := newInstaller(t)
	contains(t, f.resolve("HTTP_SET=1").stdout, "MODE=http DOMAIN=\n")
}
func TestInstallerDomainAndHTTPAreExclusive(t *testing.T) {
	f := newInstaller(t)
	contains(t, f.shell("DOMAIN_SET=1 IP_SET=0 HTTP_SET=1; prepare_config", false).stderr, "--http")
}
func TestInstallerExistingHTTPInstallKeepsHTTP(t *testing.T) {
	f := newInstaller(t)
	writeFile(t, f.env, "PANEL_PUBLIC_URL=http://127.0.0.1:8080\nPANEL_SUB_URL=http://192.0.2.1:8081\n")
	contains(t, f.resolve(":").stdout, "MODE=http DOMAIN=\n")
}
func TestInstallerExistingHTTPSIPInstallKeepsIPMode(t *testing.T) {
	f := newInstaller(t)
	writeFile(t, f.env, "PANEL_PUBLIC_URL=http://127.0.0.1:8080\nPANEL_SUB_URL=https://192.0.2.1:8443\n")
	contains(t, f.resolve(":").stdout, "MODE=ip DOMAIN=\n")
}
func TestInstallerModeChangeRequiresReconfigure(t *testing.T) {
	f := newInstaller(t)
	content := "PANEL_PUBLIC_URL=http://127.0.0.1:8080\nPANEL_SUB_URL=https://192.0.2.1:8443\n"
	writeFile(t, f.env, content)
	contains(t, f.shell("DOMAIN_SET=0 IP_SET=0 HTTP_SET=1; prepare_config; configure_panel", false).stderr, "--reconfigure")
	equal(t, readFile(t, f.env), content)
}
func TestInstallerReconfigureIPSwitchesDomainToHTTPSIP(t *testing.T) {
	f := newInstaller(t)
	writeFile(t, f.env, "PANEL_PUBLIC_URL=https://vpn.example.com:8443\nPANEL_SUB_URL=https://vpn.example.com:8443\n")
	f.shell("DOMAIN= DOMAIN_SET=0 IP_SET=1 RECONFIGURE=1; prepare_config; configure_panel", true)
	contains(t, readFile(t, f.env), "PANEL_PUBLIC_URL=http://127.0.0.1:8080\n")
	contains(t, readFile(t, f.env), "PANEL_SUB_URL=https://192.0.2.1:8443\n")
}
func TestInstallerIPCaddySiteServesOnlySubscriptions(t *testing.T) {
	f := newInstaller(t)
	site := f.shell("MODE=ip; caddy_site", true).stdout
	for _, text := range []string{"https://192.0.2.1:8443 {", "issuer acme {", "profile shortlived", "disable_tlsalpn_challenge", "reverse_proxy 127.0.0.1:8081"} {
		contains(t, site, text)
	}
	excludes(t, site, "8080")
	excludes(t, site, "vpn.example.com")
}
func TestInstallerOldCaddyIsRefusedForIPCertificates(t *testing.T) {
	f := newInstaller(t)
	f.stub("caddy", `echo "v2.6.2 h1:abc"`)
	f.stub("apt-get", "exit 0")
	f.stub("apt-cache", `printf 'caddy:\n  Candidate: 2.6.2-6\n'`)
	contains(t, f.shell("download() { :; }; gpg() { :; }; MODE=ip; install_caddy", false, true).stderr, "2.10.2")
}
func TestInstallerNewCaddyIsAcceptedForIPCertificates(t *testing.T) {
	f := newInstaller(t)
	f.stub("caddy", `echo "v2.11.4 h1:abc"`)
	f.stub("apt-get", "exit 99")
	f.shell("MODE=ip; install_caddy", true, true)
}
func TestInstallerUnissuedIPCertificateFailsWithHint(t *testing.T) {
	f := newInstaller(t)
	f.stub("curl", "exit 7")
	f.stub("sleep", "exit 0")
	r := f.shell("MODE=ip; wait_tls; summary", false)
	excludes(t, r.stdout, "giocondo-vpn is installed.")
	contains(t, r.stderr, "80/tcp")
	contains(t, r.stderr, "--http")
}
func TestInstallerIssuedIPCertificateIsVerifiedAgainstPublicIP(t *testing.T) {
	f := newInstaller(t)
	log := filepath.Join(f.base, "curl.log")
	f.stub("curl", `echo "$*" > `+quote(log)+`; for a; do last=$a; done; printf %s "${last##*=}"`)
	f.shell("MODE=ip; wait_tls", true)
	args := readFile(t, log)
	contains(t, args, "--connect-to 192.0.2.1:8443:127.0.0.1:8443")
	contains(t, args, "https://192.0.2.1:8443/health/challenge?challenge=")
	excludes(t, args, "--insecure")
	excludes(t, args, " -k ")
}
func TestInstallerCustomCaddyWithDefaultRootIsProtected(t *testing.T) {
	f := newInstaller(t)
	content := ":80 {\n root * /usr/share/caddy\n}\nother.example {\n respond \"keep\"\n}\n"
	writeFile(t, f.caddy, content)
	f.shell("configure_caddy", false)
	equal(t, readFile(t, f.caddy), content)
}
func TestInstallerLegacyMarkerIsNotOwnership(t *testing.T) {
	f := newInstaller(t)
	content := "# Managed by giocondo-vpn install.sh\nother.example { respond \"keep\" }\n"
	writeFile(t, f.caddy, content)
	f.shell("configure_caddy", false)
	equal(t, readFile(t, f.caddy), content)
}
func TestInstallerGeneratedCaddyIsReusableButEditsAreProtected(t *testing.T) {
	f := newInstaller(t)
	f.shell("configure_caddy; configure_caddy", true)
	content := readFile(t, f.caddy) + "\n# manual change\n"
	writeFile(t, f.caddy, content)
	f.shell("configure_caddy", false)
	equal(t, readFile(t, f.caddy), content)
}
func TestInstallerPackageDefaultCaddyIsReplaced(t *testing.T) {
	f := newInstaller(t)
	f.packageDefaultCaddy()
	f.shell("configure_caddy", true)
	contains(t, readFile(t, f.caddy), "https://vpn.example.com:8443")
}
func TestInstallerInvalidCaddyDoesNotReplaceFile(t *testing.T) {
	f := newInstaller(t)
	f.packageDefaultCaddy()
	f.stub("caddy", "exit 1")
	f.shell("configure_caddy", false)
	equal(t, readFile(t, f.caddy), ":80 { file_server }\n")
}
func TestInstallerReloadFailureRestoresFile(t *testing.T) {
	f := newInstaller(t)
	f.shell("configure_caddy", true)
	content := readFile(t, f.caddy)
	f.stub("systemctl", `[ "$1" != reload-or-restart ]`)
	f.shell("DOMAIN=new.example.com; configure_caddy", false)
	equal(t, readFile(t, f.caddy), content)
}
func TestInstallerCAPreparedEvenWithDownloader(t *testing.T) {
	f := newInstaller(t)
	log := filepath.Join(f.base, "apt.log")
	f.stub("apt-get", `echo "$*" >> `+quote(log))
	f.rebuildCA()
	f.shell("prepare_dependencies", true)
	contains(t, readFile(t, log), "ca-certificates")
}
func TestInstallerBootstrapFailureIsNotSuccess(t *testing.T) {
	f := newInstaller(t)
	f.stub("apt-get", "exit 1")
	f.shell("prepare_dependencies", false)
}
func TestInstallerExistingHTTPSWithoutArgumentsKeepsDomain(t *testing.T) {
	f := newInstaller(t)
	writeFile(t, f.env, "PANEL_PUBLIC_URL=https://vpn.example.com:8443\nPANEL_SUB_URL=https://vpn.example.com:8443\n")
	r := f.shell(`DOMAIN= IP= DOMAIN_SET=0 IP_SET=0; detect_ip() { echo 192.0.2.1; }; prepare_config; printf "DOMAIN=%s\n" "$DOMAIN"`, true)
	contains(t, r.stdout, "DOMAIN=vpn.example.com")
}
func TestInstallerConflictingDomainIsRefusedBeforeChanges(t *testing.T) {
	f := newInstaller(t)
	content := "PANEL_PUBLIC_URL=https://old.example.com:8443\nPANEL_SUB_URL=https://old.example.com:8443\n"
	writeFile(t, f.env, content)
	contains(t, f.shell("DOMAIN_SET=1 IP_SET=0; prepare_config", false).stderr, "--reconfigure")
	equal(t, readFile(t, f.env), content)
}
func TestInstallerReconfigureRequiresExplicitAddress(t *testing.T) {
	f := newInstaller(t)
	f.shell("RECONFIGURE=1 DOMAIN_SET=0 IP_SET=0; prepare_config", false)
}
func TestInstallerMissingBundleIsRebuiltWithoutReinstallingCA(t *testing.T) {
	f := newInstaller(t)
	f.stub("dpkg-query", `printf 'install ok installed'`)
	f.stub("apt-get", "exit 99")
	f.rebuildCA()
	f.shell("prepare_dependencies", true)
	if len(readFile(t, filepath.Join(f.base, "ca.crt"))) == 0 {
		t.Fatal("CA bundle was not rebuilt")
	}
}
func TestInstallerReadyCADoesNotInvokePackageManager(t *testing.T) {
	f := newInstaller(t)
	writeFile(t, filepath.Join(f.base, "ca.crt"), f.ca)
	f.stub("dpkg-query", `printf 'install ok installed'`)
	f.stub("apt-get", "exit 99")
	f.stub("update-ca-certificates", "exit 99")
	f.shell("prepare_dependencies", true)
}
func TestInstallerReconfigurePreservesImplicitPublicGRPC(t *testing.T) {
	f := newInstaller(t)
	writeFile(t, f.env, "PANEL_PUBLIC_URL=http://127.0.0.1:8080\n# PANEL_GRPC_LISTEN=:9443\n")
	f.shell("RECONFIGURE=1; configure_panel", true)
	excludes(t, readFile(t, f.env), "\nPANEL_GRPC_LISTEN=")
}
func TestInstallerFirstFailureAfterNodeInstallCanBeRetried(t *testing.T) {
	f := newInstaller(t)
	writeFile(t, f.env, "# package defaults\n")
	f.shell(`EXISTING=0
backup_install
printf 'PANEL_PUBLIC_URL=http://127.0.0.1:8080\nPANEL_SUB_URL=http://192.0.2.1:8081\n' > "$ENV_FILE"
printf cert > "$PKI/server.crt"
printf key > "$PKI/server.key"
printf agent > "$AGENT_CERT"
printf key > "$(dirname "$AGENT_CERT")/agent.key"
printf ca > "$(dirname "$AGENT_CERT")/ca.crt"
false`, false)
	equal(t, exists(t, filepath.Join(f.base, "pki/server.crt")), true)
	contains(t, readFile(t, f.env), "PANEL_SUB_URL=http://192.0.2.1:8081")
	f.shell("DOMAIN= DOMAIN_SET=0 IP_SET=0; prepare_config; configure_panel", true)
	contains(t, readFile(t, f.env), "PANEL_SUB_URL=http://192.0.2.1:8081")
}
func TestInstallerCorruptNonemptyBundleIsRebuilt(t *testing.T) {
	f := newInstaller(t)
	writeFile(t, filepath.Join(f.base, "ca.crt"), "broken CA")
	f.stub("dpkg-query", `printf 'install ok installed'`)
	f.stub("apt-get", "exit 99")
	f.rebuildCA()
	f.shell("prepare_dependencies", true)
	equal(t, readFile(t, filepath.Join(f.base, "ca.crt")), f.ca)
}
func TestInstallerCABootstrapWithCurlWgetOrNoDownloader(t *testing.T) {
	for _, downloader := range []string{"curl", "wget", "none"} {
		t.Run(downloader, func(t *testing.T) {
			f := newInstaller(t)
			tools := filepath.Join(f.base, "tools")
			mkdir(t, tools)
			for _, name := range []string{"grep", "openssl", "cp"} {
				path, err := exec.LookPath(name)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path, filepath.Join(tools, name)); err != nil {
					t.Fatal(err)
				}
			}
			log := filepath.Join(tools, "apt.log")
			stub(t, tools, "apt-get", `echo "$*" >> `+quote(log))
			stub(t, tools, "dpkg-query", "exit 1")
			stub(t, tools, "update-ca-certificates", "cp "+quote(filepath.Join(f.base, "ca-source.crt"))+" "+quote(filepath.Join(f.base, "ca.crt")))
			if downloader != "none" {
				stub(t, tools, downloader, "exit 0")
			}
			f.shell("PATH="+quote(tools)+"; prepare_dependencies", true)
			installed := readFile(t, log)
			contains(t, installed, "ca-certificates")
			equal(t, strings.Contains(installed, " curl"), downloader == "none")
		})
	}
}
func TestInstallerFailedReadinessCannotPrintSuccess(t *testing.T) {
	f := newInstaller(t)
	f.stub("giocondo-vpn-panel", "exit 1")
	r := f.shell("wait_ready; summary", false)
	excludes(t, r.stdout, "giocondo-vpn is installed.")
	contains(t, r.stderr, "journalctl")
}
