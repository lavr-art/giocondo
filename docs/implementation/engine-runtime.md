# Extending the node agent with engine drivers

The first runtime release supports sing-box only. Xray and AmneziaWG are not
installed, started or exposed by this change. The panel still generates the
same sing-box JSON and delivers it using the existing `ApplyConfig` command.

## Driver boundary

`internal/service/engine.Driver` adapts an external service: identity, version,
configuration validation, restart, stop, activity check and destructive traffic
collection. Drivers must honor contexts. They must not return configuration
contents or credentials in errors. Core libraries, panel code and SQLite do not
belong in the agent dependency graph.

The common `Runtime` owns files, hashes, the apply journal, rollback and ordering
of counter reads against restarts. Drivers do not write managed configuration
files. The sing-box driver retains the existing binary, configuration, command
and statistics settings. Its statistics service remains loopback-only.

To add another engine in a future release:

1. Implement `Driver` and real configuration/statistics compatibility tests.
2. Register a runtime in the agent entrypoint with a unique ID, separate config
   and state directories, local commands and its own stats endpoint.
3. Supply the binary, systemd unit and explicitly permitted writable paths.
4. Add panel rendering, supported profile selection, client subscription handling
   and capability-gated delivery in that engine's implementation. This release
   does not define a generic remote apply command or support runtime plugin loading.

`internal/worker/enginequeue.Manager` provides one apply worker per engine, one current operation and one
replaceable pending configuration. Duplicate submissions share a future; a
superseded pending operation completes with an error. Different engines have
independent workers and collection loops. A single goroutine sends Sync messages.
External operations do not run in that sender. Each Sync session waits for the outcomes of the configs it
submitted and reports them as soon as they complete; an apply outlives a dropped session:
after reconnect `Hello` shows it in the engine state, and resending the same config
shares the running operation and reports its outcome. Network alias
configuration has its own bounded worker in `internal/worker/netqueue`: one command runs,
the newest one waits, and a replaced command is reported as superseded.

## Apply and recovery

Each runtime stores `journal.json` under its state directory. The journal keeps
the confirmed hash, rejected hash, last apply error and operation phase. It has
no user registry or traffic database. Configuration files and backups can contain
keys and are written with mode `0600`; journal directories use `0700`.

| Durable phase | Recovery after process termination |
| --- | --- |
| No journal | Adopt the existing config without restarting the service. |
| `activating`, `restoring` (and legacy `prepared`) | Restore `.prev`, restart and check the previous service; if there was no previous config, stop the service and remove the failed config. |
| `committed`, active file matches | Keep it without restarting. |
| `committed`, active file changed or removed outside the agent | Re-adopt it as the confirmed config without restarting and report `config changed outside agent`. |
| Invalid journal or failed recovery | Report `blocked`; reject new applies for this engine until recovery succeeds. |

Current agents write only the `activating` phase before activation; `prepared`,
written by earlier agents, is still recovered.

A configuration changed outside the agent is never a reason to block: the panel
is the source of truth. Both `Open` and the next apply durably re-adopt the
external file, which becomes the rollback target, and the panel sees a differing
applied hash and sends its configuration again. A successful apply clears the
reported error.

The runtime durably writes the journal before replacing the active configuration.
It commits the new hash only after restart and activity checks succeed. An
interruption before that commit conservatively rolls back, even if the new
service had already started. Recovery itself is repeatable after another crash.
Temporary candidate files are not evidence of an applied configuration.

A normal apply failure attempts bounded rollback even during agent shutdown.
The runtime reads counters before each managed restart or stop and serializes
those reads with periodic collection. Unacknowledged deltas remain in memory:
agent termination can still lose them. The journal does not add durable traffic
delivery or panel-side durable sequence deduplication.

A successful systemd activity check proves local process startup, not VPN
reachability from a user's network. Existing external probes remain necessary.

## Installation and compatibility

The default `AGENT_STATE_DIR` is `/var/lib/giocondo-vpn/agent`; each engine gets a
subdirectory. A lifetime file lock excludes a second agent using the same state
directory. `AGENT_STOP_CMD` defaults to `systemctl stop giocondo-vpn-sing-box`;
operators with custom restart/status commands must also configure a matching stop
command. Existing env values and credentials are preserved on upgrade.

The agent service provisions its state directory using systemd `StateDirectory`
and retains `MemoryMax=64M`. A custom state location also needs an appropriate
systemd `ReadWritePaths` override. Do not run two agents with different state
directories managing the same service or config.

`Hello` advertises `engine-runtime-v1`. `Hello` and `Health` include per-engine
version, activity, phase, confirmed and rejected hashes, and error. Legacy
sing-box fields mirror this state. Old message field numbers and JSON hashes
are unchanged. The new panel preserves recovery errors and suppresses applies
to blocked engines. Without capabilities it uses the existing agent behavior.
Arbitrary version combinations are not guaranteed; update the panel before the
agent.

For an upgrade, preserve the database, node identity, env, PKI, configuration,
certificate state and state directory. Existing WG server/peer keys and addresses
remain in the panel database; the agent does not regenerate them. On first start
it adopts the existing configuration. A legitimate configuration change from the
panel can still require a sing-box restart.

If recovery is blocked, fix the underlying service, filesystem or backup problem
and restart the agent to retry recovery. Do not delete the journal to hide an
error. For corrupted or inconsistent state, restore matching config and state
backups while the agent is stopped. Before downgrading, finish recovery and verify
a `committed` journal whose hash matches the active file; an old agent cannot
interpret unfinished operations. Purging the agent package removes its journal
along with agent certificates/settings; it does not purge the sing-box package.

## Verification

Tests in `internal/service/engine` cover apply, unchanged-config suppression,
validation and startup failures, rollback (including after cancellation),
rollback failures, journal write errors, counter collection before every
restart, adoption and re-adoption of external configs, recovery of a legacy
`prepared` journal and interruption of a subprocess at each durable phase.
`internal/repository/enginefs` covers singleton locking; `internal/worker/enginequeue`
and `internal/worker/panelsync` cover worker isolation, superseded requests and
responsive Sync during restart/network operations.
Run `make build test lint` and `go test -race ./...`. Run the Docker network bench
and real sing-box validator checks as described in the repository instructions.
Measure RSS on Linux separately from the engine processes; the 20–30 MB agent
budget is a target, not a claim about every workload.

Implementation verification on 2026-10-30 passed the build and Go suite, the full
race suite, golangci-lint v2.15.0 (the CI version), and real sing-box config and
subscription checks. The Docker network bench passed protocol traffic, DNS
switching, SSH migration with unchanged clients and rejection of an unreachable
candidate. The installer bench passed clean installation, an idempotent second
run, and recovery of a stopped agent without replacing credentials.

The separate baseline-agent upgrade check preserved the complete server config,
client configurations and sing-box process. Five agent RSS samples were
16,986,112 bytes (about 16.2 MiB) on the Docker Linux node constrained to 1 CPU /
1 GB RAM, with the agent service's 64 MiB memory limit retained. This small
fixture does not establish memory consumption under production load. Subprocess
interruption tests exercise process crashes, not physical power loss.

For the isolated legacy-agent upgrade check, build a baseline Linux agent from
an earlier checkout into `bin/network-e2e/agent-legacy`, run the network bench,
then run `sh test/network-e2e/run.sh agent-upgrade`. The check replaces binaries
only inside the bench node, archives its existing runtime journal to simulate a
pre-runtime installation, and verifies unchanged client configurations, real
traffic and an unchanged sing-box process. It limits that container to 1 CPU /
1 GB RAM and writes RSS samples to `bin/network-e2e/agent-upgrade-results.json`.
