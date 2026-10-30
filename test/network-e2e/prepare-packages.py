"""Select checksum-verified real candidate debs and, for upgrades, the previous stable release."""
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import sys
import urllib.request

ARCH, MODE = sys.argv[1:]
WORK = Path('bin/network-e2e')
SOURCE = Path(os.environ.get('CANDIDATE_DEBS', 'dist'))
REPO = 'lavr-art/giocondo'
ROLES = ('panel', 'agent', 'sing-box')


def request(url):
    headers = {'User-Agent': 'giocondo-upgrade-tests'}
    token = os.environ.get('GH_TOKEN')
    if token and url.startswith('https://api.github.com/'):
        headers['Authorization'] = 'Bearer ' + token
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=120) as response:
        return response.read()


def select(source, destination):
    sums = {}
    for line in (source / 'SHA256SUMS').read_text().splitlines():
        digest, name = line.split(maxsplit=1)
        sums[name.lstrip('*')] = digest
    files = []
    for role in ROLES:
        matches = list(source.glob(f'giocondo-vpn-{role}_*_{ARCH}.deb'))
        if len(matches) != 1:
            raise RuntimeError(f'expected exactly one real {role}/{ARCH} deb in {source}; build with make deb')
        package = matches[0]
        if hashlib.sha256(package.read_bytes()).hexdigest() != sums.get(package.name):
            raise RuntimeError('package checksum mismatch: ' + package.name)
        files.append(package)
    if destination.exists():
        shutil.rmtree(destination)
    destination.mkdir(parents=True)
    for package in files:
        shutil.copy2(package, destination / package.name)
    shutil.copy2(source / 'SHA256SUMS', destination / 'SHA256SUMS')


select(SOURCE, WORK / 'candidate')
if MODE == 'upgrade':
    tag = os.environ.get('BASELINE_VERSION')
    if not tag:
        releases = json.loads(request(f'https://api.github.com/repos/{REPO}/releases?per_page=100'))
        # The candidate has not been published yet. GitHub returns releases newest first.
        tag = next(r['tag_name'] for r in releases if not r['draft'] and not r['prerelease'])
    if not re.fullmatch(r'v\d+\.\d+\.\d+', tag):
        raise RuntimeError('baseline must be a stable vX.Y.Z release')
    cache = WORK / 'downloads' / tag
    cache.mkdir(parents=True, exist_ok=True)
    for name in ['SHA256SUMS'] + [f'giocondo-vpn-{r}_{tag[1:]}_{ARCH}.deb' for r in ROLES]:
        target = cache / name
        if not target.exists():
            data = request(f'https://github.com/{REPO}/releases/download/{tag}/{name}')
            target.write_bytes(data)
    select(cache, WORK / 'baseline')
    (WORK / 'baseline-version').write_text(tag[1:])
