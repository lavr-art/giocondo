# Controlled upgrades and recovery

## Supported route

The supported starting point is the previous stable release. Update the panel first, then remote agents.
The release tests exercise the new panel with the previous stable agent, then upgrade the agent and core.
An agent newer than its panel, skipped releases and arbitrary downgrades are not guaranteed to work.
Keep protobuf field numbers stable and gate new behavior on advertised capabilities; a version string alone
does not establish protocol compatibility. Server sing-box and client subscription formats are separate contracts.

Re-run `install.sh` or use APT to update. Re-running the same installed version preserves settings and
does not restart healthy services. Upgrading packages interrupts services briefly; existing client profiles
remain valid, but connections may reconnect. An update is not a zero-downtime deployment.

Crossing a minor version before 1.0 (for example, 0.1 → 0.2), or a major version after 1.0,
requires an explicit opt-in, independent of `--yes`:

```sh
sh install.sh --allow-breaking-upgrade --yes
# Or, for a direct package update:
sudo env GIOCONDO_ALLOW_BREAKING_UPGRADE=1 apt-get install giocondo-vpn-panel
```

Read the release notes before allowing this transition. Ordinary downgrades are refused even with this opt-in.
This guard belongs to the incoming packages and therefore also protects the first update from an older package.
It does not prevent an administrator from forcibly installing an unprotected historic package.

## What the packages protect

Before unpacking an updated project package, its incoming preinst hook validates the version transition,
stops active panel/agent/core/Caddy writers, and creates a root-only snapshot under
`/var/lib/giocondo-vpn/backups/upgrade.*`. It then resumes the previously active services.
There may be multiple snapshots during an APT operation; each records its actual installed package versions.
The first snapshot precedes the first project package change. Failed/incomplete snapshots are retained for
diagnosis, but only snapshots with valid `SHA256SUMS` are usable for recovery.

Snapshots include panel SQLite files and WAL companions, PKI, env, agent credentials, core configuration,
engine journal, sing-box certificate state, installer state, Caddy state, and local service overrides.
Custom database/PKI/agent paths are read from the env files and, when running, the services' effective environments.
Use absolute literal paths: unsupported expansions are refused rather than evaluated as root.
Snapshots are not rotated automatically. Monitor free disk space and archive or delete old copies yourself;
they contain private keys and tokens and need the same protection as the live installation.

Failure to stop a writer or complete a snapshot prevents unpacking that package. A later APT failure can still
leave a partially upgraded package set; this is not an atomic multi-package transaction or automatic rollback.
Package restart and readiness failures return errors instead of reporting a successful update.

After initialization the panel's `check -pid PID` verifies its HTTP challenge endpoint without opening/migrating
the database. The agent's `-check -pid PID` checks the effective config, running core and committed journal
without applying changes. An offline panel does not make a locally functioning core unhealthy: the agent check
explicitly does not confirm panel synchronization. The all-in-one installer additionally verifies a fresh local
heartbeat and matching desired/applied/on-disk hashes. None of these checks proves public VPN reachability.

New applications refuse newer database schemas before changing journal mode or executing SQL migrations.
Never edit an applied SQL migration; add a new numbered file and an old-database preservation test.

## Manual recovery

Recovery loses changes made after the selected snapshot. Take affected nodes out of administration first.
Choose a complete snapshot from before the failed update; inspect `versions.tsv` and `transition.tsv` to
confirm it contains the intended package set. Do not restore an older database underneath a running service.
`package-status.tsv` also records absent/removed packages. Remove any project packages introduced after that
snapshot, without purging their data, before restoring; the tool refuses additional installed project roles.

The following commands run as root. Substitute the actual snapshot directory. On an agent-only machine,
copy `/usr/share/giocondo-vpn/agent/recover.sh` instead of the panel tool.

```sh
snapshot=/var/lib/giocondo-vpn/backups/upgrade.EXAMPLE
cp /usr/share/giocondo-vpn/panel/recover.sh /root/giocondo-recover.sh
sh /root/giocondo-recover.sh verify "$snapshot"
cat "$snapshot/versions.tsv"
```

Download the exact packages in `versions.tsv` into an otherwise empty recovery directory. Historic packages
remain in the APT pool; for each recorded row use `apt-get download PACKAGE=VERSION`. Downloading is safe
while services run. Verify downloaded packages against the signed APT repository or their original release checksums.

Then stop all installed writers, including Caddy if installed. Unpack **all** recorded packages without configuring
them. This distinction matters: historic postinst scripts may start a service immediately.

```sh
systemctl stop giocondo-vpn-agent giocondo-vpn-panel giocondo-vpn-sing-box
# If installed:
systemctl stop caddy

cd /root/giocondo-recovery-packages
GIOCONDO_RESTORE_FROM="$snapshot" dpkg --unpack ./*.deb
sh /root/giocondo-recover.sh restore-state "$snapshot"
dpkg --configure -a
systemctl daemon-reload
while IFS= read -r service; do systemctl start "$service"; done < "$snapshot/active-services"
```

The recovery tool refuses to overwrite state while a writer is active or installed versions differ from the
snapshot. It verifies checksums, removes stale SQLite WAL companions, and replaces saved directories rather
than merging them with files introduced by the failed release. The explicit recovery environment permits only
versions recorded in the selected snapshot when the incoming package supports the guard.

Check service status, the panel, subscriptions and real VPN traffic afterward. On an all-in-one host run:

```sh
giocondo-vpn-panel local-node -check -db /var/lib/giocondo-vpn/panel/panel.db -timeout 120s
```

Use the configured database and agent certificate paths for a customized installation. Keep snapshots until
recovery and client traffic are confirmed. Remote-node snapshots are local to each node; restoring a panel
does not restore remote node files or recover unacknowledged in-memory traffic deltas.

## Release verification

Release order is source tests/lint → GoReleaser candidate → actual package installation/upgrade/recovery tests
on amd64 and arm64 → draft release with those same artifacts → signed APT publication → public release.
No packages are rebuilt after verification. Failures block publication; if APT publication fails the release
remains a draft. GitHub Actions workflows must be enabled for these gates to run.
Client JSON is checked with the candidate core and the supported sing-box 1.12.0 floor; Mihomo YAML is checked
with pinned Mihomo 1.19.0. `sh test/client-validators.sh` runs these external validators explicitly.

Locally:

```sh
make deb
sh test/network-e2e/run.sh installer
BASELINE_VERSION=v0.1.4 sh test/network-e2e/run.sh upgrade
BASELINE_VERSION=v0.1.4 UPGRADE_PATH=apt sh test/network-e2e/run.sh upgrade
```

Without `BASELINE_VERSION`, the upgrade runner downloads the latest published non-prerelease/non-draft release.
`CANDIDATE_DEBS` overrides the candidate directory (default `dist`); its `SHA256SUMS` is required.
The bench verifies checksums and uses real GoReleaser packages and all three client protocols.
Public ACME issuance and external routing still require separate tests on a reachable server.
