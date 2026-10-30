"""Extract actual native candidate binaries; verify all three packages belong to one release."""
from pathlib import Path
import shutil
import subprocess
import tempfile

root = Path('/work')
arch = subprocess.check_output(['dpkg', '--print-architecture'], text=True).strip()
versions = set()
for role, binary in [('panel', 'usr/bin/giocondo-vpn-panel'), ('agent', 'usr/bin/giocondo-vpn-agent'),
                     ('sing-box', 'usr/lib/giocondo-vpn/sing-box')]:
    packages = list((root / 'candidate').glob(f'giocondo-vpn-{role}_*_{arch}.deb'))
    assert len(packages) == 1, (role, packages)
    package = packages[0]
    actual_arch = subprocess.check_output(['dpkg-deb', '-f', str(package), 'Architecture'], text=True).strip()
    assert actual_arch == arch, actual_arch
    versions.add(subprocess.check_output(['dpkg-deb', '-f', str(package), 'Version'], text=True).strip())
    with tempfile.TemporaryDirectory() as tmp:
        subprocess.run(['dpkg-deb', '-x', str(package), tmp], check=True)
        pending = root / (role + '.new')
        shutil.copy2(Path(tmp) / binary, pending)
        pending.replace(root / role)
assert len(versions) == 1, versions
(root / 'candidate-version').write_text(versions.pop())
