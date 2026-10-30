package installer

import (
	"archive/tar"
	"context"
	"database/sql"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type upgradeFixture struct {
	t                *testing.T
	root, bin, state string
}

func requireLinuxHooks(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Debian package hooks require GNU tar/coreutils; run in the Linux bench")
	}
}

func newUpgrade(t *testing.T) *upgradeFixture {
	t.Helper()
	requireLinuxHooks(t)
	dpkg, err := exec.LookPath("dpkg")
	if err != nil {
		t.Fatal("Debian package hook regressions require dpkg: ", err)
	}
	root := t.TempDir()
	f := &upgradeFixture{t: t, root: root, bin: filepath.Join(root, "bin"), state: filepath.Join(root, "var/lib/giocondo-vpn")}
	mkdir(t, f.bin)
	// Keep Debian version semantics real; only package inventory and services are isolated.
	f.stub("dpkg", `[ "$1" = --compare-versions ] || exit 1
exec `+quote(dpkg)+` "$@"`)
	f.stub("dpkg-query", `[ "$3" = giocondo-vpn-panel ] || exit 1
case "$2" in *Status-Status*Version*) printf 'installed\t0.1.4';; *Status-Status*) printf installed;; *) printf '0.1.4';; esac`)
	f.stub("systemctl", `echo "$*" >> "$TEST_ROOT/calls"
case "$1" in
 is-active) [ -e "$TEST_ROOT/active" ]; exit;;
 stop) [ ! -e "$TEST_ROOT/stop-fails" ] || exit 1; rm -f "$TEST_ROOT/active";;
 start) touch "$TEST_ROOT/active";;
esac`)
	// Recovery operates only on this fixture's archived paths. Avoid requiring root
	// merely to pass its operator privilege check in an otherwise isolated test.
	f.stub("id", `[ "$1" = -u ] || exit 1; printf '0\n'`)
	mkdir(t, filepath.Join(f.state, "panel"))
	writeFile(t, filepath.Join(f.state, "panel/panel.db"), "old database")
	mkdir(t, filepath.Join(root, "etc/giocondo-vpn"))
	mkdir(t, filepath.Join(root, "run/systemd/system"))
	writeFile(t, filepath.Join(root, "active"), "")
	return f
}
func (f *upgradeFixture) stub(name, body string) { f.t.Helper(); stub(f.t, f.bin, name, body) }
func (f *upgradeFixture) environment(extra ...string) []string {
	f.t.Helper()
	env := []string{"PATH=" + f.bin + string(os.PathListSeparator) + os.Getenv("PATH"), "TEST_ROOT=" + f.root, "DPKG_MAINTSCRIPT_PACKAGE=giocondo-vpn-panel", "GIOCONDO_RESTORE_FROM=", "GIOCONDO_ALLOW_BREAKING_UPGRADE="}
	return append(env, extra...)
}
func (f *upgradeFixture) source(path string, prefixes ...string) string {
	f.t.Helper()
	source := readFile(f.t, filepath.Join(repositoryRoot(f.t), path))
	for _, prefix := range prefixes {
		source = strings.ReplaceAll(source, prefix, f.root+prefix)
	}
	return source
}
func (f *upgradeFixture) hook(old, new string, extra ...string) commandResult {
	f.t.Helper()
	source := f.source("deploy/apt/deb/upgrade-preinst.sh", "/var/lib/", "/etc/", "/run/")
	return runCommand(f.t, f.environment(extra...), "sh", "-c", source, "preinst", "upgrade", old, new)
}
func (f *upgradeFixture) backups() []string {
	f.t.Helper()
	sums, err := filepath.Glob(filepath.Join(f.state, "backups/upgrade.*/SHA256SUMS"))
	if err != nil {
		f.t.Fatal(err)
	}
	for i := range sums {
		sums[i] = filepath.Dir(sums[i])
	}
	return sums
}
func (f *upgradeFixture) snapshot() string {
	f.t.Helper()
	f.hook("0.1.4", "0.1.5").requireSuccess(f.t)
	backups := f.backups()
	equal(f.t, len(backups), 1)
	return backups[0]
}
func (f *upgradeFixture) recover(backup string) commandResult {
	f.t.Helper()
	if err := os.Remove(filepath.Join(f.root, "active")); err != nil && !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
	return runCommand(f.t, f.environment(), "sh", filepath.Join(repositoryRoot(f.t), "deploy/apt/recover.sh"), "restore-state", backup)
}
func archiveNames(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	archive := tar.NewReader(file)
	var names []string
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
}
func requireArchivePath(t *testing.T, backup, path string) {
	t.Helper()
	want := strings.TrimPrefix(path, "/")
	for _, name := range archiveNames(t, filepath.Join(backup, "state.tar")) {
		if name == want {
			return
		}
	}
	t.Fatalf("snapshot missing %q", want)
}

func TestUpgradeSnapshotBeforeUnpackIncludesWALAndCustomPaths(t *testing.T) {
	f := newUpgrade(t)
	custom := filepath.Join(f.root, "custom db")
	mkdir(t, custom)
	writeFile(t, filepath.Join(custom, "data.db"), "custom db")
	wal := filepath.Join(custom, "data.db-wal")
	writeFile(t, wal, "pending writes")
	writeFile(t, filepath.Join(f.root, "etc/giocondo-vpn/panel.env"), "PANEL_DB=\""+filepath.Join(custom, "data.db")+"\"\n")
	backup := f.snapshot()
	requireArchivePath(t, backup, wal)
	equal(t, readFile(t, filepath.Join(backup, "versions.tsv")), "giocondo-vpn-panel\t0.1.4\n")
	info, err := os.Stat(backup)
	if err != nil {
		t.Fatal(err)
	}
	equal(t, info.Mode().Perm(), os.FileMode(0700))
	equal(t, exists(t, filepath.Join(f.root, "active")), true)
}
func TestUpgradeBreakingTransitionRefusedBeforeStop(t *testing.T) {
	requireLinuxHooks(t)
	for _, version := range []string{"0.2.0", "1.0.0", "2.0.0"} {
		t.Run(version, func(t *testing.T) {
			f := newUpgrade(t)
			result := f.hook("0.1.4", version)
			result.requireFailure(t)
			contains(t, result.stderr, "GIOCONDO_ALLOW_BREAKING_UPGRADE")
			equal(t, exists(t, filepath.Join(f.root, "calls")), false)
			equal(t, len(f.backups()), 0)
		})
	}
}
func TestUpgradeBreakingOptInStillCreatesBackup(t *testing.T) {
	f := newUpgrade(t)
	f.hook("0.1.4", "0.2.0", "GIOCONDO_ALLOW_BREAKING_UPGRADE=1").requireSuccess(t)
	equal(t, len(f.backups()), 1)
}
func TestUpgradeDowngradeRefusedEvenWithBreakingOptIn(t *testing.T) {
	f := newUpgrade(t)
	r := f.hook("0.1.4", "0.1.3", "GIOCONDO_ALLOW_BREAKING_UPGRADE=1")
	r.requireFailure(t)
	contains(t, r.stderr, "downgrade")
	equal(t, exists(t, filepath.Join(f.root, "calls")), false)
}
func TestUpgradeStopFailurePreventsSnapshot(t *testing.T) {
	f := newUpgrade(t)
	writeFile(t, filepath.Join(f.root, "stop-fails"), "")
	f.hook("0.1.4", "0.1.5").requireFailure(t)
	equal(t, len(f.backups()), 0)
}
func TestUpgradeCopyFailurePreservesServicesAndIsNotComplete(t *testing.T) {
	f := newUpgrade(t)
	f.stub("tar", "exit 1")
	f.hook("0.1.4", "0.1.5").requireFailure(t)
	equal(t, len(f.backups()), 0)
	equal(t, exists(t, filepath.Join(f.root, "active")), true)
}
func TestUpgradePartialChecksumWriteIsNotACompleteSnapshot(t *testing.T) {
	f := newUpgrade(t)
	f.stub("sha256sum", `printf 'partial checksum output\n'; exit 1`)
	f.hook("0.1.4", "0.1.5").requireFailure(t)
	equal(t, len(f.backups()), 0)
}
func TestUpgradeNoSnapshotOrRestartForSameVersion(t *testing.T) {
	f := newUpgrade(t)
	f.hook("0.1.4", "0.1.4").requireSuccess(t)
	equal(t, len(f.backups()), 0)
	equal(t, exists(t, filepath.Join(f.root, "calls")), false)
}
func TestUpgradeSettingsAreNotExecuted(t *testing.T) {
	f := newUpgrade(t)
	marker := filepath.Join(f.root, "executed")
	writeFile(t, filepath.Join(f.root, "etc/giocondo-vpn/panel.env"), "PANEL_DB=$(touch "+marker+")\n")
	f.hook("0.1.4", "0.1.5").requireFailure(t)
	equal(t, exists(t, marker), false)
}
func TestUpgradeArchiveCanRestoreFilesAlsoListedUnderParentDirectories(t *testing.T) {
	f := newUpgrade(t)
	nested := filepath.Join(f.root, "etc/giocondo-vpn/agent")
	mkdir(t, nested)
	secret := filepath.Join(nested, "agent.key")
	writeFile(t, secret, "original-key")
	backup := f.snapshot()
	writeFile(t, secret, "changed-key")
	runCommand(t, f.environment(), "tar", "--recursive-unlink", "-C", "/", "-xpf", filepath.Join(backup, "state.tar")).requireSuccess(t)
	equal(t, readFile(t, secret), "original-key")
}
func TestUpgradeAgentRestartFailureIsPropagated(t *testing.T) {
	requireLinuxHooks(t)
	for _, withCertificate := range []bool{true, false} {
		name := "custom_certificate_path"
		if withCertificate {
			name = "default_certificate_path"
		}
		t.Run(name, func(t *testing.T) {
			f := newUpgrade(t)
			cert := filepath.Join(f.root, "etc/giocondo-vpn/agent/agent.crt")
			mkdir(t, filepath.Dir(cert))
			if withCertificate {
				writeFile(t, cert, "")
			}
			writeFile(t, filepath.Join(f.root, "etc/giocondo-vpn/agent.env"), "AGENT_PANEL_ADDR=panel:9443\n")
			f.stub("systemctl", `[ "$1" != restart ] || exit 42`)
			helper := filepath.Join(f.root, "usr/share/giocondo-vpn/agent/postflight.sh")
			mkdir(t, filepath.Dir(helper))
			stub(t, filepath.Dir(helper), "postflight.sh", "exit 0")
			source := f.source("deploy/apt/deb/agent-postinst.sh", "/var/lib/", "/etc/", "/run/", "/usr/share/")
			r := runCommand(t, f.environment(), "sh", "-c", source, "postinst", "configure", "0.1.4")
			equal(t, r.code, 42)
		})
	}
}
func TestUpgradeCorruptedSnapshotIsRefusedByRecovery(t *testing.T) {
	f := newUpgrade(t)
	backup := f.snapshot()
	writeFile(t, filepath.Join(backup, "state.tar"), "corrupt")
	runCommand(t, f.environment(), "sh", filepath.Join(repositoryRoot(t), "deploy/apt/recover.sh"), "verify", backup).requireFailure(t)
}
func TestUpgradeVerifiedRecoveryRequiresRecordedTargetVersion(t *testing.T) {
	f := newUpgrade(t)
	backup := f.snapshot()
	f.hook("0.1.5", "0.1.4", "GIOCONDO_RESTORE_FROM="+backup).requireSuccess(t)
	denied := f.hook("0.1.5", "0.1.3", "GIOCONDO_RESTORE_FROM="+backup)
	denied.requireFailure(t)
	contains(t, denied.stderr, "does not match")
}
func TestUpgradeSymlinkDatabaseIncludesCanonicalWAL(t *testing.T) {
	f := newUpgrade(t)
	target := filepath.Join(f.root, "external/data.db")
	mkdir(t, filepath.Dir(target))
	writeFile(t, target, "database")
	wal := target + "-wal"
	writeFile(t, wal, "committed WAL transactions")
	alias := filepath.Join(f.root, "alias.db")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, "etc/giocondo-vpn/panel.env"), "PANEL_DB="+alias+"\n")
	backup := f.snapshot()
	requireArchivePath(t, backup, wal)
	equal(t, strings.TrimSpace(readFile(t, filepath.Join(backup, "database-path"))), target)
}
func TestUpgradeRealSymlinkSQLiteWALRecoversCommittedTransactions(t *testing.T) {
	f := newUpgrade(t)
	target := filepath.Join(f.root, "external/data.db")
	mkdir(t, filepath.Dir(target))
	alias := filepath.Join(f.root, "alias.db")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", alias)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "CREATE TABLE records (value TEXT)", "INSERT INTO records VALUES ('original committed value')"} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	equal(t, exists(t, target+"-wal"), true)
	writeFile(t, filepath.Join(f.root, "etc/giocondo-vpn/panel.env"), "PANEL_DB="+alias+"\n")
	backup := f.snapshot()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, target+"-wal", "stale newer WAL")
	f.recover(backup).requireSuccess(t)
	restored, err := sql.Open("sqlite", alias)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restored.Close(); err != nil {
			t.Error(err)
		}
	})
	rows, err := restored.QueryContext(ctx, "SELECT value FROM records")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	})
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	equal(t, len(values), 1)
	equal(t, values[0], "original committed value")
}
func TestUpgradeRecoveryRefusesANewlyIntroducedPackage(t *testing.T) {
	f := newUpgrade(t)
	backup := f.snapshot()
	f.stub("dpkg-query", `case "$2" in *Status-Status*) printf installed;; *) printf '0.1.4';; esac`)
	r := f.recover(backup)
	r.requireFailure(t)
	contains(t, r.stderr, "remove newly introduced package giocondo-vpn-agent")
}
func TestUpgradeDPKGPreinstHalfInstalledStateRecordsOldVersion(t *testing.T) {
	f := newUpgrade(t)
	f.stub("dpkg-query", `[ "$3" = giocondo-vpn-panel ] || exit 1
printf 'half-installed\t0.1.4'`)
	backup := f.snapshot()
	equal(t, readFile(t, filepath.Join(backup, "versions.tsv")), "giocondo-vpn-panel\t0.1.4\n")
}
func TestUpgradeRecoveryRefusesRemovedPackageEvenIfVersionMatches(t *testing.T) {
	f := newUpgrade(t)
	backup := f.snapshot()
	f.stub("dpkg-query", `[ "$3" = giocondo-vpn-panel ] || exit 1
case "$2" in *Status-Status*) printf config-files;; *) printf '0.1.4';; esac`)
	r := f.recover(backup)
	r.requireFailure(t)
	contains(t, r.stderr, "unpack the recorded package giocondo-vpn-panel")
}
