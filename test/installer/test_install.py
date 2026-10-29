"""Installer regressions using real temporary files and isolated command stubs.

Run: python3 -m unittest discover -s test/installer -v
"""
import hashlib
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
SOURCE = (ROOT / 'install.sh').read_text().rsplit('main "$@"', 1)[0]


class InstallerTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.certs = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.certs.cleanup)
        cert = Path(cls.certs.name) / 'ca.crt'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                        '-subj', '/CN=Installer test CA', '-keyout', str(cert.with_suffix('.key')),
                        '-out', str(cert)], check=True, capture_output=True)
        cls.ca = cert.read_text()

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        (self.base / 'bin').mkdir()
        self.stub('caddy', 'exit 0')
        self.stub('systemctl', 'exit 0')
        self.stub('dpkg-query', 'exit 1')
        self.env = self.base / 'panel.env'
        self.caddy = self.base / 'Caddyfile'
        (self.base / 'pki').mkdir()
        (self.base / 'ca-source.crt').write_text(self.ca)

    def stub(self, name, body):
        path = self.base / 'bin' / name
        path.write_text('#!/bin/sh\n' + body + '\n')
        path.chmod(0o755)

    def shell(self, body, success=True, real_caddy_install=False):
        prefix = f'''
ENV_FILE={shlex.quote(str(self.env))}
CADDYFILE={shlex.quote(str(self.caddy))}
INSTALL_STATE={shlex.quote(str(self.base / 'installer'))}
CA_BUNDLE={shlex.quote(str(self.base / 'ca.crt'))}
PKI={shlex.quote(str(self.base / 'pki'))}
AGENT_CERT={shlex.quote(str(self.base / 'agent.crt'))}
DOMAIN=vpn.example.com IP=192.0.2.1 MODE=domain RECONFIGURE=0 EXISTING=1 PANEL_CHANGED=0 YES=1 HTTP_SET=0
'''
        if not real_caddy_install:
            prefix += 'install_caddy() { :; }\n'
        result = subprocess.run(['sh', '-eu', '-c', SOURCE + prefix + body], text=True, capture_output=True,
                                env={**os.environ, 'PATH': str(self.base / 'bin') + ':' + os.environ['PATH']})
        if success:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout)
        return result

    def test_repeat_preserves_existing_settings(self):
        content = ('PANEL_PUBLIC_URL=https://vpn.example.com:8443\n'
                   'PANEL_SUB_URL=https://vpn.example.com:8443\n'
                   'PANEL_GRPC_LISTEN=:9443\nPANEL_LISTEN=127.0.0.1:9090\n')
        self.env.write_text(content)
        self.shell('DOMAIN=; configure_panel')
        self.assertEqual(self.env.read_text(), content)

    def test_reconfigure_preserves_grpc(self):
        self.env.write_text('PANEL_GRPC_LISTEN=:9555\nPANEL_LISTEN=127.0.0.1:8080\n')
        self.shell('RECONFIGURE=1; configure_panel')
        self.assertIn('PANEL_GRPC_LISTEN=:9555\n', self.env.read_text())

    def test_reconfigure_ip_urls_use_existing_listener_ports(self):
        self.env.write_text('PANEL_LISTEN=127.0.0.1:9090\nPANEL_SUB_LISTEN=127.0.0.1:9091\n')
        self.shell('DOMAIN= MODE=http RECONFIGURE=1; configure_panel')
        self.assertIn('PANEL_PUBLIC_URL=http://127.0.0.1:9090\n', self.env.read_text())
        self.assertIn('PANEL_SUB_URL=http://192.0.2.1:9091\n', self.env.read_text())

    def resolve(self, args):
        return self.shell('DOMAIN= IP= DOMAIN_SET=0 IP_SET=0; detect_ip() { echo 192.0.2.1; }; '
                          f'{args}; prepare_config; printf "MODE=%s DOMAIN=%s\\n" "$MODE" "$DOMAIN"')

    def test_new_install_defaults_to_https_by_ip(self):
        self.env.write_text('PANEL_LISTEN=127.0.0.1:9090\nPANEL_SUB_LISTEN=127.0.0.1:9091\n')
        result = self.shell('DOMAIN= IP= DOMAIN_SET=0 IP_SET=0 EXISTING=0; detect_ip() { echo 192.0.2.1; }; '
                            'prepare_config; configure_panel; printf "MODE=%s\\n" "$MODE"')
        self.assertIn('MODE=ip', result.stdout)
        env = self.env.read_text()
        self.assertIn('PANEL_PUBLIC_URL=http://127.0.0.1:9090\n', env)
        self.assertIn('PANEL_SUB_URL=https://192.0.2.1:8443\n', env)
        self.assertIn('PANEL_SUB_LISTEN=127.0.0.1:9091\n', env)

    def test_http_flag_keeps_plain_http(self):
        self.assertIn('MODE=http DOMAIN=\n', self.resolve('HTTP_SET=1').stdout)

    def test_domain_and_http_are_exclusive(self):
        result = self.shell('DOMAIN_SET=1 IP_SET=0 HTTP_SET=1; prepare_config', success=False)
        self.assertIn('--http', result.stderr)

    def test_existing_http_install_keeps_http(self):
        self.env.write_text('PANEL_PUBLIC_URL=http://127.0.0.1:8080\nPANEL_SUB_URL=http://192.0.2.1:8081\n')
        self.assertIn('MODE=http DOMAIN=\n', self.resolve(':').stdout)

    def test_existing_https_ip_install_keeps_ip_mode(self):
        self.env.write_text('PANEL_PUBLIC_URL=http://127.0.0.1:8080\nPANEL_SUB_URL=https://192.0.2.1:8443\n')
        self.assertIn('MODE=ip DOMAIN=\n', self.resolve(':').stdout)

    def test_mode_change_requires_reconfigure(self):
        content = 'PANEL_PUBLIC_URL=http://127.0.0.1:8080\nPANEL_SUB_URL=https://192.0.2.1:8443\n'
        self.env.write_text(content)
        result = self.shell('DOMAIN_SET=0 IP_SET=0 HTTP_SET=1; prepare_config; configure_panel', success=False)
        self.assertIn('--reconfigure', result.stderr)
        self.assertEqual(self.env.read_text(), content)

    def test_reconfigure_ip_switches_domain_to_https_ip(self):
        self.env.write_text('PANEL_PUBLIC_URL=https://vpn.example.com:8443\nPANEL_SUB_URL=https://vpn.example.com:8443\n')
        self.shell('DOMAIN= DOMAIN_SET=0 IP_SET=1 RECONFIGURE=1; prepare_config; configure_panel')
        env = self.env.read_text()
        self.assertIn('PANEL_PUBLIC_URL=http://127.0.0.1:8080\n', env)
        self.assertIn('PANEL_SUB_URL=https://192.0.2.1:8443\n', env)

    def test_ip_caddy_site_serves_only_subscriptions(self):
        site = self.shell('MODE=ip; caddy_site').stdout
        self.assertIn('https://192.0.2.1:8443 {', site)
        self.assertIn('issuer acme {', site)
        self.assertIn('profile shortlived', site)
        self.assertIn('disable_tlsalpn_challenge', site)
        self.assertIn('reverse_proxy 127.0.0.1:8081', site)
        self.assertNotIn('8080', site)
        self.assertNotIn('vpn.example.com', site)

    def test_old_caddy_is_refused_for_ip_certificates(self):
        self.stub('caddy', 'echo "v2.6.2 h1:abc"')
        self.stub('apt-get', 'exit 0')
        self.stub('apt-cache', "printf 'caddy:\\n  Candidate: 2.6.2-6\\n'")
        result = self.shell('download() { :; }; gpg() { :; }; MODE=ip; install_caddy', success=False, real_caddy_install=True)
        self.assertIn('2.10.2', result.stderr)

    def test_new_caddy_is_accepted_for_ip_certificates(self):
        self.stub('caddy', 'echo "v2.11.4 h1:abc"')
        self.stub('apt-get', 'exit 99')
        self.shell('MODE=ip; install_caddy', real_caddy_install=True)

    def test_unissued_ip_certificate_fails_with_hint(self):
        self.stub('curl', 'exit 7')
        self.stub('sleep', 'exit 0')
        result = self.shell('MODE=ip; wait_tls; summary', success=False)
        self.assertNotIn('giocondo-vpn is installed.', result.stdout)
        self.assertIn('80/tcp', result.stderr)
        self.assertIn('--http', result.stderr)

    def test_issued_ip_certificate_is_verified_against_public_ip(self):
        log = self.base / 'curl.log'
        self.stub('curl', f'echo "$*" > {shlex.quote(str(log))}; for a; do last=$a; done; printf %s "${{last##*=}}"')
        self.shell('MODE=ip; wait_tls')
        args = log.read_text()
        self.assertIn('--connect-to 192.0.2.1:8443:127.0.0.1:8443', args)
        self.assertIn('https://192.0.2.1:8443/health/challenge?challenge=', args)
        self.assertNotIn('--insecure', args)
        self.assertNotIn(' -k ', args)

    def test_custom_caddy_with_default_root_is_protected(self):
        content = ':80 {\n root * /usr/share/caddy\n}\nother.example {\n respond "keep"\n}\n'
        self.caddy.write_text(content)
        self.shell('configure_caddy', success=False)
        self.assertEqual(self.caddy.read_text(), content)

    def test_legacy_marker_is_not_ownership(self):
        content = '# Managed by giocondo-vpn install.sh\nother.example { respond "keep" }\n'
        self.caddy.write_text(content)
        self.shell('configure_caddy', success=False)
        self.assertEqual(self.caddy.read_text(), content)

    def test_generated_caddy_is_reusable_but_edits_are_protected(self):
        self.shell('configure_caddy; configure_caddy')
        generated = self.caddy.read_text()
        self.caddy.write_text(generated + '\n# manual change\n')
        self.shell('configure_caddy', success=False)
        self.assertEqual(self.caddy.read_text(), generated + '\n# manual change\n')

    def test_package_default_caddy_is_replaced(self):
        self.caddy.write_text(':80 { file_server }\n')
        md5 = hashlib.md5(self.caddy.read_bytes()).hexdigest()
        self.stub('dpkg-query', f"printf ' {self.caddy} {md5}\\n'")
        self.shell('configure_caddy')
        self.assertIn('https://vpn.example.com:8443', self.caddy.read_text())

    def test_invalid_caddy_does_not_replace_file(self):
        self.caddy.write_text(':80 { file_server }\n')
        md5 = hashlib.md5(self.caddy.read_bytes()).hexdigest()
        self.stub('dpkg-query', f"printf ' {self.caddy} {md5}\\n'")
        self.stub('caddy', 'exit 1')
        self.shell('configure_caddy', success=False)
        self.assertEqual(self.caddy.read_text(), ':80 { file_server }\n')

    def test_reload_failure_restores_file(self):
        self.shell('configure_caddy')
        original = self.caddy.read_text()
        self.stub('systemctl', '[ "$1" != reload-or-restart ]')
        self.shell('DOMAIN=new.example.com; configure_caddy', success=False)
        self.assertEqual(self.caddy.read_text(), original)

    def test_ca_prepared_even_with_downloader(self):
        log = self.base / 'apt.log'
        self.stub('apt-get', f'echo "$*" >> {shlex.quote(str(log))}')
        self.stub('update-ca-certificates', f'cp {shlex.quote(str(self.base / "ca-source.crt"))} {shlex.quote(str(self.base / "ca.crt"))}')
        self.stub('dpkg-query', 'exit 1')
        self.shell('prepare_dependencies')
        self.assertIn('ca-certificates', log.read_text())

    def test_bootstrap_failure_is_not_success(self):
        self.stub('apt-get', 'exit 1')
        self.shell('prepare_dependencies', success=False)

    def test_existing_https_without_arguments_keeps_domain(self):
        self.env.write_text('PANEL_PUBLIC_URL=https://vpn.example.com:8443\nPANEL_SUB_URL=https://vpn.example.com:8443\n')
        result = self.shell('DOMAIN= IP= DOMAIN_SET=0 IP_SET=0; detect_ip() { echo 192.0.2.1; }; prepare_config; printf "DOMAIN=%s\\n" "$DOMAIN"')
        self.assertIn('DOMAIN=vpn.example.com', result.stdout)

    def test_conflicting_domain_is_refused_before_changes(self):
        content = 'PANEL_PUBLIC_URL=https://old.example.com:8443\nPANEL_SUB_URL=https://old.example.com:8443\n'
        self.env.write_text(content)
        result = self.shell('DOMAIN_SET=1 IP_SET=0; prepare_config', success=False)
        self.assertIn('--reconfigure', result.stderr)
        self.assertEqual(self.env.read_text(), content)

    def test_reconfigure_requires_explicit_address(self):
        self.shell('RECONFIGURE=1 DOMAIN_SET=0 IP_SET=0; prepare_config', success=False)

    def test_missing_bundle_is_rebuilt_without_reinstalling_ca(self):
        self.stub('dpkg-query', "printf 'install ok installed'")
        self.stub('apt-get', 'exit 99')
        self.stub('update-ca-certificates', f'cp {shlex.quote(str(self.base / "ca-source.crt"))} {shlex.quote(str(self.base / "ca.crt"))}')
        self.shell('prepare_dependencies')
        self.assertTrue((self.base / 'ca.crt').stat().st_size)

    def test_ready_ca_does_not_invoke_package_manager(self):
        (self.base / 'ca.crt').write_text(self.ca)
        self.stub('dpkg-query', "printf 'install ok installed'")
        self.stub('apt-get', 'exit 99')
        self.stub('update-ca-certificates', 'exit 99')
        self.shell('prepare_dependencies')

    def test_reconfigure_preserves_implicit_public_grpc(self):
        self.env.write_text('PANEL_PUBLIC_URL=http://127.0.0.1:8080\n# PANEL_GRPC_LISTEN=:9443\n')
        self.shell('RECONFIGURE=1; configure_panel')
        self.assertNotIn('\nPANEL_GRPC_LISTEN=', self.env.read_text())

    def test_first_failure_after_node_install_can_be_retried(self):
        self.env.write_text('# package defaults\n')
        self.shell('''EXISTING=0
backup_install
printf 'PANEL_PUBLIC_URL=http://127.0.0.1:8080\\nPANEL_SUB_URL=http://192.0.2.1:8081\\n' > "$ENV_FILE"
printf cert > "$PKI/server.crt"
printf key > "$PKI/server.key"
printf agent > "$AGENT_CERT"
printf key > "$(dirname "$AGENT_CERT")/agent.key"
printf ca > "$(dirname "$AGENT_CERT")/ca.crt"
false
''', success=False)
        self.assertTrue((self.base / 'pki/server.crt').exists())
        self.assertIn('PANEL_SUB_URL=http://192.0.2.1:8081', self.env.read_text())
        self.shell('DOMAIN= DOMAIN_SET=0 IP_SET=0; prepare_config; configure_panel')
        self.assertIn('PANEL_SUB_URL=http://192.0.2.1:8081', self.env.read_text())

    def test_corrupt_nonempty_bundle_is_rebuilt(self):
        (self.base / 'ca.crt').write_text('broken CA')
        self.stub('dpkg-query', "printf 'install ok installed'")
        self.stub('apt-get', 'exit 99')
        self.stub('update-ca-certificates', f'cp {shlex.quote(str(self.base / "ca-source.crt"))} {shlex.quote(str(self.base / "ca.crt"))}')
        self.shell('prepare_dependencies')
        self.assertEqual((self.base / 'ca.crt').read_text(), self.ca)

    def test_ca_bootstrap_with_curl_wget_or_no_downloader(self):
        for downloader in ('curl', 'wget', None):
            with self.subTest(downloader=downloader):
                tools = self.base / ('tools-' + str(downloader))
                tools.mkdir()
                for name in ('grep', 'openssl', 'cp'):
                    (tools / name).symlink_to(shutil.which(name))
                for name in ('apt-get', 'dpkg-query', 'update-ca-certificates'):
                    target = tools / name
                    if name == 'apt-get':
                        body = f'echo "$*" >> {shlex.quote(str(tools / "apt.log"))}'
                    elif name == 'dpkg-query':
                        body = 'exit 1'
                    else:
                        body = f'cp {shlex.quote(str(self.base / "ca-source.crt"))} {shlex.quote(str(self.base / "ca.crt"))}'
                    target.write_text('#!/bin/sh\n' + body + '\n')
                    target.chmod(0o755)
                if downloader:
                    (tools / downloader).write_text('#!/bin/sh\nexit 0\n')
                    (tools / downloader).chmod(0o755)
                self.shell(f'PATH={shlex.quote(str(tools))}; prepare_dependencies')
                installed = (tools / 'apt.log').read_text()
                self.assertIn('ca-certificates', installed)
                self.assertEqual(' curl' in installed, downloader is None)

    def test_failed_readiness_cannot_print_success(self):
        self.stub('giocondo-vpn-panel', 'exit 1')
        result = self.shell('wait_ready; summary', success=False)
        self.assertNotIn('giocondo-vpn is installed.', result.stdout)
        self.assertIn('journalctl', result.stderr)


if __name__ == '__main__':
    unittest.main()
