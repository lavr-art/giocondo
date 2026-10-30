"""Previous stable -> candidate, mixed versions, unchanged clients, and explicit recovery."""
import hashlib
import json
import os
import shlex

import installer as bench
from test import WORK, REPORT, wait

BASELINE = (WORK / 'baseline-version').read_text().strip()
CANDIDATE = (WORK / 'candidate-version').read_text().strip()
CANDIDATE_INSTALL = bench.INSTALL
assert BASELINE != CANDIDATE, 'upgrade test needs different releases'
bench.INSTALL = CANDIDATE_INSTALL.replace('/repo', '/baseline-repo')
PACKAGES = ['giocondo-vpn-panel', 'giocondo-vpn-agent', 'giocondo-vpn-sing-box']


def branch(version):
    parts = version.split('~', 1)[0].split('.')
    return parts[:2] if parts[0] == '0' else parts[:1]


BREAKING = branch(BASELINE) != branch(CANDIDATE)
ALLOWED_INSTALL = CANDIDATE_INSTALL + (' --allow-breaking-upgrade' if BREAKING else '')
ALLOW_APT = 'GIOCONDO_ALLOW_BREAKING_UPGRADE=1 ' if BREAKING else ''


def sh(command, check=True):
    return bench.sh(command, check=check)


def identity():
    output = sh('find /etc/giocondo-vpn/agent /var/lib/giocondo-vpn/panel/pki -type f -exec sha256sum {} +; '
              'sha256sum /etc/giocondo-vpn/panel.env /etc/giocondo-vpn/agent.env '
              '/etc/giocondo-vpn/sing-box/config.json').stdout
    return '\n'.join(sorted(output.splitlines()))


def traffic(selected):
    for index, kind in enumerate(sorted(selected), 1):
        wait('unchanged client after upgrade: ' + kind,
             lambda port=19100 + index: bench.client_request(port), 120)


def exercise(user, selected):
    before = identity()
    initial_user = bench.api('GET', 'users')[0]
    # Change the package source without running the all-in-one update yet.
    sh("sed -i 's|/baseline-repo|/repo|g' /etc/apt/sources.list.d/giocondo-vpn.list; apt-get update -qq")
    target = shlex.quote(CANDIDATE)
    if BREAKING:
        refused = sh(f'DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends giocondo-vpn-panel={target}', check=False)
        assert refused.returncode != 0, 'breaking upgrade succeeded without opt-in'
        assert identity() == before, 'refused transition changed identity'
        assert sh("dpkg-query -W -f='${Version}' giocondo-vpn-panel").stdout == BASELINE
    sh(f'{ALLOW_APT}DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends giocondo-vpn-panel={target}')
    old_agent = sh("dpkg-query -W -f='${Version}' giocondo-vpn-agent").stdout
    assert old_agent == BASELINE, (old_agent, BASELINE)
    sh('cp /usr/share/giocondo-vpn/panel/recover.sh /root/giocondo-recover.sh')
    wait('new panel accepts previous stable agent', lambda: bench.api('GET', 'nodes')[0]['status'] == 'online')
    traffic(selected)
    # The first snapshot precedes the first package change and contains the coherent baseline set.
    snapshot = sh('find /var/lib/giocondo-vpn/backups -mindepth 1 -maxdepth 1 -type d').stdout.strip()
    assert '\n' not in snapshot and snapshot, snapshot
    if os.environ.get('UPGRADE_PATH', 'installer') == 'apt':
        sh(ALLOW_APT + 'DEBIAN_FRONTEND=noninteractive apt-get install -y ' + ' '.join(p + '=' + target for p in PACKAGES))
    else:
        bench.INSTALL = ALLOWED_INSTALL
        output = sh(bench.INSTALL).stdout
        assert 'giocondo-vpn is installed.' in output
    bench.INSTALL = ALLOWED_INSTALL
    traffic(selected)
    assert identity() == before, 'upgrade changed keys, settings or server configuration'
    current_user = bench.api('GET', 'users')[0]
    for key in ['id', 'name', 'uuid', 'subscription_url', 'expires_at', 'traffic_limit_bytes']:
        assert current_user.get(key) == initial_user.get(key), key
    REPORT.append('previous stable upgraded with preserved identity and all three client protocols')

    # Exercise the documented restore order: unpack old packages while all writers are
    # stopped, restore state, and only then configure/start the old programs.
    recovery = '/root/giocondo-recover.sh'
    sh(f'sh {recovery} verify {shlex.quote(snapshot)}')
    sh('systemctl stop giocondo-vpn-agent giocondo-vpn-panel giocondo-vpn-sing-box')
    old_debs = sh('find /work/baseline -name "*.deb"').stdout.splitlines()
    sh('GIOCONDO_RESTORE_FROM=' + shlex.quote(snapshot) + ' dpkg --unpack ' + ' '.join(map(shlex.quote, old_debs)))
    sh(f'sh {recovery} restore-state {shlex.quote(snapshot)}')
    sh('dpkg --configure -a')
    sh('systemctl daemon-reload; while IFS= read -r service; do systemctl start "$service"; done < '
       + shlex.quote(snapshot + '/active-services'))
    traffic(selected)
    assert identity() == before, 'recovery changed identity'
    REPORT.append('manual package and state recovery restores unchanged client traffic')
    # End on the candidate so the standard repeat-run and recovery checks still apply.
    sh(ALLOWED_INSTALL)
    traffic(selected)


bench.main(exercise)
(WORK / 'upgrade-results.txt').write_text('\n'.join(REPORT) + '\n')
