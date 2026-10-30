"""Incoming package hooks: exercise real files with isolated system-command boundaries."""
import os
from pathlib import Path
import subprocess
import sqlite3
import tarfile
import tempfile
import unittest
import sys

ROOT = Path(__file__).resolve().parents[2]


@unittest.skipUnless(sys.platform.startswith('linux'), 'Debian package hooks require GNU tar/coreutils; run in the Linux bench')
class UpgradeTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.bin = self.root / 'bin'
        self.bin.mkdir()
        self.stub('dpkg', '''[ "$1" = --compare-versions ] || exit 1
python3 - "$2" "$3" "$4" <<'PY'
import sys
def v(s):return tuple(int(x) for x in s.split('.'))
a,op,b=sys.argv[1:];sys.exit(0 if {'lt':v(a)<v(b),'eq':v(a)==v(b)}[op] else 1)
PY''')
        self.stub('dpkg-query', '''[ "$3" = giocondo-vpn-panel ] || exit 1
case "$2" in *Status-Status*Version*) printf 'installed\\t0.1.4';; *Status-Status*) printf installed;; *) printf '0.1.4';; esac''')
        self.stub('systemctl', '''echo "$*" >> "$TEST_ROOT/calls"
case "$1" in
 is-active) [ -e "$TEST_ROOT/active" ]; exit;;
 stop) [ ! -e "$TEST_ROOT/stop-fails" ] || exit 1; rm -f "$TEST_ROOT/active";;
 start) touch "$TEST_ROOT/active";;
esac''')
        self.state = self.root / 'var/lib/giocondo-vpn'
        (self.state / 'panel').mkdir(parents=True)
        (self.state / 'panel/panel.db').write_bytes(b'old database')
        (self.root / 'etc/giocondo-vpn').mkdir(parents=True)
        (self.root / 'run/systemd/system').mkdir(parents=True)
        (self.root / 'active').touch()

    def stub(self, name, body):
        p = self.bin / name
        p.write_text('#!/bin/sh\n' + body + '\n')
        p.chmod(0o755)

    def run_hook(self, old='0.1.4', new='0.1.5', extra=None):
        source = (ROOT / 'deploy/apt/deb/upgrade-preinst.sh').read_text()
        for prefix in ['/var/lib/', '/etc/', '/run/']:
            source = source.replace(prefix, str(self.root) + prefix)
        return subprocess.run(['sh', '-c', source, 'preinst', 'upgrade', old, new],
                              text=True, capture_output=True, env={**os.environ,
                              'PATH': str(self.bin) + ':' + os.environ['PATH'],
                              'TEST_ROOT': str(self.root), 'DPKG_MAINTSCRIPT_PACKAGE': 'giocondo-vpn-panel',
                              **(extra or {})})

    def backups(self):
        return list((self.state / 'backups').glob('upgrade.*/SHA256SUMS'))

    def test_snapshot_before_unpack_includes_wal_and_custom_paths(self):
        custom = self.root / 'custom db'
        custom.mkdir()
        (custom / 'data.db').write_bytes(b'custom db')
        (custom / 'data.db-wal').write_bytes(b'pending writes')
        (self.root / 'etc/giocondo-vpn/panel.env').write_text(f'PANEL_DB="{custom}/data.db"\n')
        r = self.run_hook()
        self.assertEqual(r.returncode, 0, r.stderr)
        sums = self.backups()
        self.assertEqual(len(sums), 1)
        backup = sums[0].parent
        with tarfile.open(backup / 'state.tar') as archive:
            names = archive.getnames()
            self.assertIn(str(custom / 'data.db-wal').lstrip('/'), names)
        self.assertEqual((backup / 'versions.tsv').read_text(), 'giocondo-vpn-panel\t0.1.4\n')
        self.assertEqual(backup.stat().st_mode & 0o777, 0o700)
        self.assertTrue((self.root / 'active').exists())

    def test_breaking_transition_refused_before_stop(self):
        for new in ['0.2.0', '1.0.0', '2.0.0']:
            with self.subTest(new=new):
                r = self.run_hook(new=new)
                self.assertNotEqual(r.returncode, 0)
                self.assertIn('GIOCONDO_ALLOW_BREAKING_UPGRADE', r.stderr)
                self.assertFalse((self.root / 'calls').exists())
                self.assertFalse(self.backups())

    def test_breaking_opt_in_still_creates_backup(self):
        r = self.run_hook(new='0.2.0', extra={'GIOCONDO_ALLOW_BREAKING_UPGRADE': '1'})
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(len(self.backups()), 1)

    def test_downgrade_refused_even_with_breaking_opt_in(self):
        r = self.run_hook(new='0.1.3', extra={'GIOCONDO_ALLOW_BREAKING_UPGRADE': '1'})
        self.assertNotEqual(r.returncode, 0)
        self.assertIn('downgrade', r.stderr)
        self.assertFalse((self.root / 'calls').exists())

    def test_stop_failure_prevents_snapshot(self):
        (self.root / 'stop-fails').touch()
        r = self.run_hook()
        self.assertNotEqual(r.returncode, 0)
        self.assertFalse(self.backups())

    def test_copy_failure_preserves_services_and_is_not_complete(self):
        self.stub('tar', 'exit 1')
        r = self.run_hook()
        self.assertNotEqual(r.returncode, 0)
        self.assertFalse(self.backups())
        self.assertTrue((self.root / 'active').exists())

    def test_partial_checksum_write_is_not_a_complete_snapshot(self):
        self.stub('sha256sum', "printf 'partial checksum output\\n'; exit 1")
        result = self.run_hook()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.backups())

    def test_no_snapshot_or_restart_for_same_version(self):
        r = self.run_hook(new='0.1.4')
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertFalse(self.backups())
        self.assertFalse((self.root / 'calls').exists())

    def test_settings_are_not_executed(self):
        marker = self.root / 'executed'
        (self.root / 'etc/giocondo-vpn/panel.env').write_text(f'PANEL_DB=$(touch {marker})\n')
        r = self.run_hook()
        self.assertNotEqual(r.returncode, 0)
        self.assertFalse(marker.exists())

    def test_archive_can_restore_files_also_listed_under_parent_directories(self):
        nested = self.root / 'etc/giocondo-vpn/agent'
        nested.mkdir()
        secret = nested / 'agent.key'
        secret.write_text('original-key')
        r = self.run_hook()
        self.assertEqual(r.returncode, 0, r.stderr)
        secret.write_text('changed-key')
        backup = self.backups()[0].parent
        restored = subprocess.run(['tar', '--recursive-unlink', '-C', '/', '-xpf', str(backup / 'state.tar')],
                                  text=True, capture_output=True)
        self.assertEqual(restored.returncode, 0, restored.stderr)
        self.assertEqual(secret.read_text(), 'original-key')

    def test_agent_restart_failure_is_propagated(self):
        cert = self.root / 'etc/giocondo-vpn/agent/agent.crt'
        cert.parent.mkdir()
        cert.touch()
        (self.root / 'etc/giocondo-vpn/agent.env').write_text('AGENT_PANEL_ADDR=panel:9443\n')
        self.stub('systemctl', '[ "$1" != restart ] || exit 42')
        helper = self.root / 'usr/share/giocondo-vpn/agent/postflight.sh'
        helper.parent.mkdir(parents=True)
        helper.write_text('#!/bin/sh\nexit 0\n')
        helper.chmod(0o755)
        source = (ROOT / 'deploy/apt/deb/agent-postinst.sh').read_text()
        for prefix in ['/var/lib/', '/etc/', '/run/', '/usr/share/']:
            source = source.replace(prefix, str(self.root) + prefix)
        r = subprocess.run(['sh', '-c', source, 'postinst', 'configure', '0.1.4'],
                           env={**os.environ, 'PATH': str(self.bin) + ':' + os.environ['PATH']}, capture_output=True)
        self.assertEqual(r.returncode, 42)
        # A service using custom certificate paths must still restart the candidate.
        cert.unlink()
        r = subprocess.run(['sh', '-c', source, 'postinst', 'configure', '0.1.4'],
                           env={**os.environ, 'PATH': str(self.bin) + ':' + os.environ['PATH']}, capture_output=True)
        self.assertEqual(r.returncode, 42)

    def test_corrupted_snapshot_is_refused_by_recovery(self):
        self.assertEqual(self.run_hook().returncode, 0)
        backup = self.backups()[0].parent
        (backup / 'state.tar').write_bytes(b'corrupt')
        r = subprocess.run(['sh', str(ROOT / 'deploy/apt/recover.sh'), 'verify', str(backup)], capture_output=True)
        self.assertNotEqual(r.returncode, 0)

    def test_verified_recovery_requires_recorded_target_version(self):
        self.assertEqual(self.run_hook().returncode, 0)
        backup = self.backups()[0].parent
        allowed = self.run_hook(old='0.1.5', new='0.1.4', extra={'GIOCONDO_RESTORE_FROM':str(backup)})
        self.assertEqual(allowed.returncode, 0, allowed.stderr)
        denied = self.run_hook(old='0.1.5', new='0.1.3', extra={'GIOCONDO_RESTORE_FROM':str(backup)})
        self.assertNotEqual(denied.returncode, 0)
        self.assertIn('does not match', denied.stderr)

    def test_symlink_database_includes_canonical_wal(self):
        target = self.root / 'external/data.db'
        target.parent.mkdir()
        target.write_bytes(b'database')
        wal = target.with_name('data.db-wal')
        wal.write_bytes(b'committed WAL transactions')
        alias = self.root / 'alias.db'
        alias.symlink_to(target)
        (self.root / 'etc/giocondo-vpn/panel.env').write_text(f'PANEL_DB={alias}\n')
        r = self.run_hook()
        self.assertEqual(r.returncode, 0, r.stderr)
        backup = self.backups()[0].parent
        with tarfile.open(backup / 'state.tar') as archive:
            self.assertIn(str(wal).lstrip('/'), archive.getnames())
        self.assertEqual((backup / 'database-path').read_text().strip(), str(target))

    def recover(self, backup):
        (self.root / 'active').unlink(missing_ok=True)
        return subprocess.run(['sh', str(ROOT / 'deploy/apt/recover.sh'), 'restore-state', str(backup)],
                              capture_output=True, text=True, env={**os.environ,
                              'PATH': str(self.bin) + ':' + os.environ['PATH'], 'TEST_ROOT': str(self.root)})

    def test_real_symlink_sqlite_wal_recovers_committed_transactions(self):
        target = self.root / 'external/data.db'
        target.parent.mkdir()
        alias = self.root / 'alias.db'
        alias.symlink_to(target)
        db = sqlite3.connect(alias)
        self.addCleanup(db.close)
        db.execute('PRAGMA journal_mode=WAL')
        db.execute('CREATE TABLE records (value TEXT)')
        db.execute("INSERT INTO records VALUES ('original committed value')")
        db.commit()
        self.assertTrue(Path(str(target) + '-wal').exists())
        (self.root / 'etc/giocondo-vpn/panel.env').write_text(f'PANEL_DB={alias}\n')
        r = self.run_hook()
        self.assertEqual(r.returncode, 0, r.stderr)
        backup = self.backups()[0].parent
        db.close()
        # Simulate the new installation leaving an unrelated WAL beside the canonical file.
        Path(str(target) + '-wal').write_bytes(b'stale newer WAL')
        result = self.recover(backup)
        self.assertEqual(result.returncode, 0, result.stderr)
        with sqlite3.connect(alias) as restored:
            self.assertEqual(restored.execute('SELECT value FROM records').fetchall(), [('original committed value',)])

    def test_recovery_refuses_a_newly_introduced_package(self):
        self.assertEqual(self.run_hook().returncode, 0)
        backup = self.backups()[0].parent
        self.stub('dpkg-query', '''case "$2" in *Status-Status*) printf installed;; *) printf '0.1.4';; esac''')
        result = self.recover(backup)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('remove newly introduced package giocondo-vpn-agent', result.stderr)

    def test_dpkg_preinst_half_installed_state_records_old_version(self):
        self.stub('dpkg-query', '''[ "$3" = giocondo-vpn-panel ] || exit 1
printf 'half-installed\\t0.1.4' ''')
        result = self.run_hook()
        self.assertEqual(result.returncode, 0, result.stderr)
        backup = self.backups()[0].parent
        self.assertEqual((backup / 'versions.tsv').read_text(), 'giocondo-vpn-panel\t0.1.4\n')

    def test_recovery_refuses_removed_package_even_if_version_matches(self):
        self.assertEqual(self.run_hook().returncode, 0)
        backup = self.backups()[0].parent
        self.stub('dpkg-query', '''[ "$3" = giocondo-vpn-panel ] || exit 1
case "$2" in *Status-Status*) printf config-files;; *) printf '0.1.4';; esac''')
        result = self.recover(backup)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('unpack the recorded package giocondo-vpn-panel', result.stderr)


if __name__ == '__main__':
    unittest.main()
