"""Validate the generated reverse proxy with real Caddy and a local TLS certificate.

Runs inside the disposable installer container; no public ACME service is needed.
"""
from pathlib import Path
import re
import subprocess
import tempfile
import time

library = Path('/src/install.sh').read_text().rsplit('main "$@"', 1)[0]


def generated_site(mode, ip):
    return subprocess.check_output(['sh', '-eu', '-c', library + f'\nMODE={mode} DOMAIN=localhost IP={ip}\ncaddy_site\n'],
                                   text=True)


def curl(cert, host, path):
    # Like wait_tls: the request names the public host but connects through loopback.
    return subprocess.run(['curl', '-sS', '--max-time', '2', '--cacert', str(cert), '-o', '/dev/stdout',
                           '--connect-to', f'{host}:8443:127.0.0.1:8443',
                           '-w', '\n%{http_code}', f'https://{host}:8443{path}'], capture_output=True, text=True)


def check(mode, host):
    # A non-loopback IP: a connection without SNI must not depend on the local address matching the certificate.
    site = generated_site(mode, host if mode == 'ip' else '127.0.0.1')
    if mode == 'ip':
        assert 'profile shortlived' in site, site
        # Replace the ACME issuer with a local fixture; matchers and backends stay as generated.
        site, replaced = re.subn(r'\ttls \{.*?\n\t\}\n', '', site, flags=re.S)
        assert replaced == 1, site
    with tempfile.TemporaryDirectory(prefix='installer-caddy-') as tmp:
        root = Path(tmp)
        cert, key = root / 'cert.pem', root / 'key.pem'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                        '-subj', '/CN=localhost', '-addext', f'subjectAltName=DNS:localhost,IP:{host if mode == "ip" else "127.0.0.1"}',
                        '-keyout', str(key), '-out', str(cert)], check=True, capture_output=True)
        config = root / 'Caddyfile'
        fixture_site = site.replace('{\n', '{\n admin off\n auto_https disable_redirects\n', 1)
        config.write_text(fixture_site.rsplit('}', 1)[0] + f' tls {cert} {key}\n}}\n')
        subprocess.run(['caddy', 'validate', '--config', str(config), '--adapter', 'caddyfile'],
                       check=True, capture_output=True)
        log = root / 'caddy.log'
        stream = log.open('w')
        process = subprocess.Popen(['caddy', 'run', '--config', str(config), '--adapter', 'caddyfile'],
                                   stdout=stream, stderr=stream)
        try:
            expected = 'd' * 64
            for _ in range(20):
                result = curl(cert, host, f'/health/challenge?challenge={expected}')
                if result.returncode == 0 and result.stdout == expected + '\n200':
                    break
                time.sleep(0.5)
            else:
                raise AssertionError(f'generated {mode} Caddy HTTPS proxy failed: ' + result.stderr + '\n' + log.read_text())
            admin = curl(cert, host, '/')
            if mode == 'ip':
                assert admin.stdout.endswith('\n404'), f'ip mode exposes the admin UI: {admin.stdout}'
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
            stream.close()
    print(f'PASS generated {mode} Caddy HTTPS proxy with verified local TLS')


check('domain', 'localhost')
check('ip', '192.0.2.10')
