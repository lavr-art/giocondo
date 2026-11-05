# CLAUDE.md

Working rules for AI agents in the giocondo (giocondo-vpn) repository.
Before making changes, study the affected subsystems, their tests and contracts. Look for the
answer in the repository first (`rg`, `rg --files`), then in external documentation.
Do not invent paths, commands, capabilities or implementation status based on an old plan.

## Purpose and stack

giocondo-vpn is a control panel for VPN nodes for personal use, replacing Hiddify Manager.
The panel stores the state, agents serve the nodes, and users get subscriptions
for Hiddify, Happ, v2rayNG and other compatible clients.

- Backend: Go 1.26+; the exact version and dependencies are in `go.mod`,
  module `github.com/artemlavr/giocondo`. HTTP is the standard `net/http`.
- Database: SQLite via `modernc.org/sqlite`, no CGO. PostgreSQL is not implemented yet;
  keep SQL portable, but do not assume support for another dialect.
- Web: `html/template` + htmx, templates and static files embedded via `embed`.
  There is no SPA, React, npm build or separate frontend module.
- Nodes: sing-box only; VLESS Reality, Hysteria2, WireGuard.
  The core version is pinned in `build/sing-box/go.mod`, separately from the root module.
- Link: an outbound gRPC `Sync` stream from the agent with mTLS and a private CA.
- Build: `CGO_ENABLED=0`; debs for Linux amd64/arm64 via GoReleaser,
  a signed apt repository on GitHub Pages.

## Repository map

| Path | Responsibility |
| --- | --- |
| `cmd/panel/` | Panel entry point and CLI: CA, tokens, local-node, provision, probe |
| `internal/app/panelapp/` | Cached dependency construction, HTTP/gRPC wiring and panel resource lifecycle |
| `cmd/agent/` | Agent startup and a check of its dependency boundary |
| `internal/service/` | Business operations, validation and consumer-owned dependency interfaces |
| `internal/worker/` | Background schedules/queues: `watchsched`, `healthsched`, `events`, `reconciliation`, `tasks`; on the agent the engine apply queue (`enginequeue`), host network queue (`netqueue`) and the Sync loop (`panelsync`) |
| `internal/transport/http/api/` | REST adapter with domain-specific service interfaces, route registry and token scopes |
| `internal/transport/http/web/` | Web adapter with domain-specific service interfaces, sessions, templates, `static/app.js`, `static/style.css` |
| `internal/model/panel/`, `internal/model/protocol/` | Entities, access policy, protocol parameter models, protocol keys and UUIDs |
| `internal/repository/sqlite/` | Database open, migration runner and shared helpers (`DB.InTx`, `MapErr`, `Time`); a package per domain beneath it (`users`, `tokens`, `nodes`, `network`, `traffic`, `probes`, `hosts`): `repository.go` with the struct and constructor, one file per query |
| `migrations/` | SQL schema migrations `NNNN_*.sql`, embedded by `migrations.FS` |
| `internal/service/nodeconfig/` | Desired config preparation, model conversion, WG and monitor credentials |
| `internal/service/nodestate/` | Agent admission, reported node/installation state, desired config hashes, traffic records, reconciliation pass |
| `internal/transport/grpc/nodehub/` | Sync server: stream loop, pb conversion, config delivery and throttling, `Kick` subscriptions |
| `internal/app/agent/` | Agent config, cached dependency construction and lifecycle; the Sync loop is `internal/worker/panelsync` |
| `internal/service/engine/`, `internal/repository/enginefs/`, `internal/platform/singboxctl/` | Agent engine runtime (apply journal, rollback, recovery), its durable files and the sing-box command/statistics driver |
| `internal/platform/hostnet/`, `internal/platform/metrics/`, `internal/clients/panel/`, `internal/model/nodesync/` | Agent host network rules, host metrics, outgoing panel gRPC client and transport-independent Sync messages |
| `internal/service/pki/`, `internal/repository/pkifs/`, `internal/transport/grpc/nodeauth/` | Certificate issuance, PEM persistence/TLS loading and verified gRPC identities |
| `internal/transport/grpc/pb/`, `internal/transport/grpc/v2raystats/` | Generated protobuf/gRPC contracts |
| `internal/service/singbox/` | Server config model and generator, TLS certificates, WG addresses |
| `internal/service/subscription/`, `internal/transport/http/subapi/` | Subscriptions, links, sing-box JSON, Mihomo YAML, routing; the `GET /sub/<token>` handler |
| `internal/service/provisioning/`, `internal/platform/installation/`, `internal/clients/ssh/` | Installation policy/jobs, embedded installer artifacts and SSH transport |
| `internal/clients/cloudflare/`, `internal/platform/protocheck/`, `internal/service/nodeprobe/`, `internal/clients/probepoint/`, `internal/transport/http/probeapi/` | Cloudflare DNS; local protocol checks, node probes, the external probe point client and its server API |
| `internal/worker/events/`, `internal/clients/webhook/` | In-memory event queue/retries and HTTP delivery |
| `proto/` | Source `.proto` files; generated with `make proto` |
| `deploy/apt/` | Delivery via deb packages and the apt repository: deb scripts, systemd units, embedded agent installer, repository script |
| `install.sh` | One-line server installer (panel + local node), published to GitHub Pages on release |
| `test/network-e2e/` | Network Docker bench |
| `build/sing-box/` | A separate Go module for our own core build |
| `pkg/logger` | `FromCtx`: the process logger (`slog.Default`) with the correlation attributes of a context; `With` stores them |
| `.github/workflows/` | CI and releases |

Task documentation:

- `docs/api.yaml` is the REST API specification; a test checks it against `api.Routes`.
- `docs/release.md` covers package releases and apt repository setup.
- `Makefile`, `.goreleaser.yaml`, the env files in `deploy/apt/deb/` define commands and packaging.

Do not consider the Hetzner/Vultr cloud APIs, a Telegram bot or importing old WireGuard servers
implemented without code. AmneziaWG is postponed: add it only on explicit request.

## Mandatory working rules

- Fix the root cause. The diff size is determined by the correct solution:
  related migrations, shared functions and changes across several layers are fine.
  Do not mask an error with a silent fallback or by suppressing a check.
- Add only what the task needs. Leave out drive-by refactorings, settings "for the future"
  and unused abstractions.
- Ask when a significant choice is not determined by the code and the request:
  a user-visible behavior change or a real trade-off between two solutions.
  Offer a recommendation; the amount of work by itself is not a reason to stop.
- For a complex task, write a plan first. Follow decisions already agreed;
  do not ask for permission again for necessary steps within them.
- Check the working copy state before editing. Preserve other people's changes;
  do not use reset/clean and do not overwrite unrelated files.
- Minimal dependencies: the standard library and existing tools first.
  Logging is `log/slog`; panel and agent code take the logger with `logger.FromCtx(ctx)` from `pkg/logger`
  (checked by `test/architecture`), and boundaries store correlation attributes with `logger.With`
  (`request_id`, `node`, `operation`; on the agent `panel`). Do not log an error that is also returned.
  Network calls and external processes take a context and timeouts.
- Names must explain their purpose; comments explain a reason, an invariant or a constraint.
  Do not add comments that retell the code or long explanations inside functions.
- Comments, documentation, UI text and commit messages are written in English.
- Do not put secrets into code, the repository, logs, error messages or test artifacts.
  Settings go through the existing env variables with the `PANEL_`, `AGENT_` prefixes.
- Add commands, agents, skills and hooks only when the task needs them.
  If delegation is needed, use the built-in agent tool, not a nested CLI through the shell.
- Do not commit, publish or deploy changes automatically.
  When asked to commit, group related changes by meaning:
  `type(area): short action`; explain the reason in the body when it is not obvious.

## Architecture and layer boundaries

The panel is the single source of truth: users, tokens, terms, limits,
WireGuard keys, nodes, profiles and traffic live in its database. A lost node
is restored from the panel state. The agent keeps the current config and the previous one
for rollback; it has no database of its own and no independent user registry.

- Implement business operations in focused `internal/service` packages with consumer-owned repository ports. API and web receive separate service interfaces; never pass the whole application to a handler or service.
- A service package keeps its ports, the service struct with private fields and its `New` constructor in `service.go`, and one file per public operation with that operation's private steps. It exports no interface of its own service; a dependency on another service is a small unexported interface in `service.go`. Services import only the shared `operation`, `singbox` and `nodeconfig` packages from `internal/service`; `test/architecture` checks it.
- Application structs keep dependencies in private fields. Methods create and cache each dependency; use the same methods for server, CLI and integration-test wiring. Do not create an `Admin` facade, an `assembly` package or a second component container.
- Split app construction by responsibility: `application.go`, `database.go`, `repositories.go`, `client.go`, `service.go`, `server.go` and `worker.go`. Prepare server dependencies before starting concurrent work; app methods must not race during lazy initialization.
- Put background loops, timers, queues and goroutine execution in `internal/worker`. Services retain single-pass business operations and depend on an executor interface for background tasks; they do not import concrete workers. App creates, starts, cancels and joins workers.
- `internal/model` owns models independently of SQLite and engine rendering. Services must not import concrete repositories, clients, platform adapters, transport, workers or application packages. Repositories, clients and platform adapters must not import services, workers or application packages; reviewed exceptions are listed with a reason. The checks are in `test/architecture`.
- Package names are unique, and module packages are imported without aliases (tests may dot-import a fixture), so a qualifier always names one package. Choose a distinct name for a new package; `test/architecture` checks both rules. Exception: a domain repository in `internal/repository/sqlite/<domain>` is named like the domain and may share its name with `internal/service/<domain>`; files that use both import the repository under an alias (`userrepo`, `noderepo`, ...).
- Create nodes and their inbounds through `CreateNodeWithInbounds` as one transaction. WG and monitor preparation after commit is retryable; do not report a committed node as a creation failure solely because preparation failed.
  Do not duplicate validation, Store changes, notifications and `Kick` in handlers.
- Configs are delivered through `nodehub` and `Sync`. Do not bypass this path
  by editing node configs directly from an HTTP handler.
- `provisioning.Runtime` is responsible for installing the agent, not for every business operation.
  LocalRuntime installs the same agent on the panel machine; the link stays mTLS.
- The agent uses `internal/app/agent`, engine services, node-sync models, outgoing clients and platform adapters. It must not transitively import panel services, panel models or SQLite.
  The check is `cmd/agent/deps_test.go`.
- No management port is opened on a node: the agent connects to the panel itself.
  When the panel is lost, sing-box keeps working with the last config.
- Node budget: 1 CPU, 1 GB RAM; the target agent RSS is 20–30 MB.
  Do not add heavy dependencies or background tasks to the agent without need.

## Sync, configuration and traffic

- The client certificate CN equals the node name in the database. An unregistered or
  disabled node is not admitted; the panel and agent certificates have different EKUs.
- `nodestate.Service` admits agents, records reported state and traffic, and prepares desired configs through
  `nodeconfig.Prepare`, which reads repository ports and calls `singbox.Build`. The hub only converts messages
  and delivers; transport does not import `nodeconfig` or `singbox` (`test/architecture`).
  Keep the protection against resending an applied or an already rejected config: it is per-stream state in the hub.
- Reconciliation runs every 30 seconds and on `Hub.Kick()`, including the desired state of offline nodes.
  Apply throttling: the first new config immediately, the following ones within the interval as one
  freshly built config at its end. Changing a limit without changing the active user set
  must not cause an extra core restart.
- `desired_hash`, `applied_hash`, `apply_error` reflect the real state;
  dirty means the desired and applied hashes differ.
- Applying: `.new` → `sing-box check` → save `.prev` → atomic replace →
  restart → startup check. On failure, roll back; an error must not be reported as success.
- Before restarting the core the agent collects the counters. Deltas are kept until `StatsAck`;
  the panel deduplicates by `instance_id` and `seq`. A repeat is acknowledged without
  being counted again. Do not lose or double traffic on reconnect.
- `AGENT_PANEL_ADDR` accepts comma-separated addresses. Keep the sticky failover
  and backoff; the certificate/SNI must match the address in use.
- On shutdown the panel cancels HTTP requests and workers, stops transports, waits for active HTTP/gRPC handlers and all nested background tasks, then closes the database. Repeated Close is safe; a rejected second Run must not stop the first.

Our own sing-box build requires `with_v2ray_api`; the build tags and parameters
in the Makefile and GoReleaser must stay consistent. The statistics service is
`v2ray.core.app.stats.command.StatsService`, default address `127.0.0.1:10085`.
WireGuard is accounted through separate direct outbounds `wg-<name>` and `source_ip_cidr` rules:
the WG endpoint has no regular per-user counters.

## Database and contract changes

- Database access lives in the domain repositories under `internal/repository/sqlite/`; a service receives a field per domain
  and declares the port it uses. A transaction belongs to one repository method. Migrations are `migrations/NNNN_*.sql`,
  applied in `sqlite.Open`. Do not edit already applied migrations;
  accompany a schema change with a new migration and a check of upgrading an old database.
- Time is UTC, written via `sqlite.Time`: fixed-width `2006-01-02T15:04:05Z`.
  Traffic days are `YYYY-MM-DD`. Do not break the string comparison of terms in SQLite.
- Resetting limited traffic changes the offsets and keeps the history.
  An extension counts from the current term, and for an expired one from the current time.
- A new or changed REST route requires consistent `api.Routes`,
  access scope, handler, `docs/api.yaml` and contract tests.
  The check is `TestOpenAPIMatchesRoutes`; the registry itself does not replace a behavior test.
- Start a protobuf change with `proto/*.proto`, then `make proto`.
  Do not edit the generated `.pb.go` by hand; update both sides of Sync.
- Align a new env setting with config parsing, tests, the env example
  and installation if it affects the package. Do not create a separate source of defaults.

## Subscriptions and client compatibility

- `GET /sub/<token>`: `?format=links|singbox|clash` takes precedence over User-Agent.
  Hiddify gets base64 links; its rule is checked before Clash/sing-box.
  Karing and sing-box clients get JSON, Mihomo-compatible ones get YAML.
- The client JSON supports sing-box 1.12+; the server core is versioned separately.
  Do not carry new server fields into client configs without a compatibility check.
- Unknown token → 404, manually disabled user → 403.
  An expired term or limit → 200 with the real `subscription-userinfo`,
  but such a user is not in the node working config. Disabled nodes are excluded.
- Keep the `subscription-userinfo`, `profile-title` with `base64:`,
  `profile-update-interval` and `content-disposition` headers in all formats.
- Routing: private networks direct; `ru-blocked` through the VPN before the RU rules;
  the ru/su/xn--p1ai zones and the corresponding RU categories direct; everything else through the VPN.
  The rule-set source is runetfreedom/russia-v2ray-rules-dat, release branch.
- Host overrides replace the node host in the subscription and respect `sort`.
  The SNI changes for Hysteria2; Reality keeps a valid `server_name`, WG has no SNI.
- Link parameters are an external contract: Hysteria2 `insecure=1`, password = UUID;
  WireGuard: private key in userinfo, `publickey`, `presharedkey`, `address`, `mtu`.
  Do not treat a parameter unknown to the client as working certificate protection.

## Web, API and security

- Admin UI/API: `PANEL_LISTEN`, default `127.0.0.1:8080`.
  Subscriptions: a separate `PANEL_SUB_LISTEN` server, `127.0.0.1:8081`;
  public address `PANEL_SUB_URL`, defaulting to `PANEL_PUBLIC_URL`.
  Do not publish admin routes on the subscription server; HTTPS is provided by the reverse proxy.
- `PANEL_ADMIN_PATH` is added to links, redirects, `HX-Redirect` and the cookie Path.
  Internal routes are registered without the prefix. Check behavior both with and without
  the prefix; a secret path does not replace authentication.
- API: Bearer tokens `vpc_…`, only the sha256 in the database, the raw token is shown once.
  Scopes `monitor` ⊂ `node-sync` ⊂ `admin`; the concrete requirement is set by `Route.Scope`.
  Missing/invalid token → 401, insufficient scope → 403. `PANEL_API_KEY` is deprecated.
- Web: `PANEL_ADMIN_PASSWORD`, a signed cookie, SameSite=Strict,
  an Origin/Sec-Fetch-Site check on POST and a limit on failed logins.
  Do not disable these checks to simplify a handler or a test.
- CSP forbids inline JS and inline CSS, including `style=`. Use the existing
  static files and classes; htmx is already in `internal/transport/http/web/static/`.
  htmx action errors are passed through `HX-Trigger` / `showError`.
- Background changes keep notifications and `Kick` when the active set changes.
  `internal/worker/events` owns in-memory queue execution and retry scheduling;
  `internal/clients/webhook` owns the HTTP request.
  Warning flags are stored in the database so events are not repeated after a restart.
  The webhook queue is in memory: do not promise guaranteed delivery.

## Provisioning, DNS and packaging

- SSH installation and cloud-init output are implemented; this is not VM creation through a cloud API.
  A password and a supplied private SSH key are used for the operation and are not stored.
  Keep the host key check and remove only the panel deploy key, not other keys.
- DNS/IP operations run through the network service with a log and state checks.
  Keep DNS record ownership, blocking of conflicting operations, retry/rollback.
  A retry is a new log entry; do not erase history and do not overwrite external changes.
- Online over Sync does not prove VPN availability. Keep the checks of real
  connections, the separate service credentials and the option of an external check point.
  Service credentials must not end up in user subscriptions.
- Panel: `/usr/bin/giocondo-vpn-panel`, `/etc/giocondo-vpn/panel.env`, data and PKI
  in `/var/lib/giocondo-vpn/panel`; the service runs as the `giocondo-vpn` user.
- Node: `/usr/bin/giocondo-vpn-agent`, `/usr/lib/giocondo-vpn/sing-box`, certificates in
  `/etc/giocondo-vpn/agent/`, env in `/etc/giocondo-vpn/agent.env`, config in
  `/etc/giocondo-vpn/sing-box/config.json`, state in `/var/lib/giocondo-vpn/sing-box`.
- When changing installation, keep embed, the deb scripts and the systemd units consistent.
  An upgrade must not overwrite the user's env or weaken key permissions.
  Do not replace our own core package with the official binary without v2ray_api.

## Tests: check behavior, not the presence of code

- For a bug, first reproduce it with a test and make sure it fails for the right reason;
  for new logic, pin down the first behavior before implementing it. Then green and refactor.
  If a test is written after the fix, check it against the original behavior
  without reverting other people's changes in the working copy.
- Every new test must catch a specific failure. Do not test a trivial getter,
  a rename, a constant or a mock call instead of the result of the real operation.
  Do not add unreachable cases or duplicates just for coverage.
- Use stdlib `testing`, tables, `t.Run`, `t.Helper`, `t.TempDir`, `httptest`
  and a real temporary SQLite following the existing examples. Mocks belong at external
  boundaries, not in place of the code under test; do not require access to production services.
- Check exact values, the response status/body, the error type, the stored state.
  "There is an error" or "the list is not empty" alone is not enough for a contract.
- Multi-service SQLite scenarios live in `test/panelintegration` and use the actual application wiring. Application tests cover dependency construction, shared instances and resource lifecycle; worker tests cover scheduling, queues, cancellation and completion.
- Golden files are the contract of the generator and subscriptions. Update them only for an intentional
  change of the output, explain the change and read the diff. Do not update a golden file to get a green test.
- For documentation edits, pure packaging or visual styling, do not create a fake test.
  Name the appropriate check and the limits of what it proves.

## Build and checks

The main set before committing code changes:

```sh
make build test lint
```

`make build` creates `bin/panel`, `bin/agent` without CGO;
`make test` runs `go test ./...`; `make lint` runs `golangci-lint run ./...`.
The linter must support the Go version from `go.mod`; CI uses v2.15.0.
The current CI runs the Go tests and lint, while the external checks below run separately.
`make verify`, `make race` and `make help` are not defined here.

| Change | Additional check |
| --- | --- |
| Concurrency, Sync, counters, background operations | `go test -race ./...` |
| Server config | `make sing-box`, then `PATH="$PWD/bin:$PATH" go test -count=1 ./internal/service/singbox` |
| Client JSON | `PATH="$PWD/bin:$PATH" SINGBOX_EXTRA=/path/to/sing-box-1.12 go test -count=1 ./internal/service/subscription` |
| Mihomo YAML | `MIHOMO_CHECK=1 go test -count=1 ./internal/service/subscription`, requires `mihomo` in PATH |
| REST contract | `go test -count=1 ./internal/transport/http/api` |
| Agent dependencies | `go test -count=1 ./cmd/agent` |
| DNS/IP, moving and node availability | `sh test/network-e2e/run.sh` in the isolated Docker bench |
| `install.sh` | `sh test/network-e2e/run.sh installer` (clean container, traffic, idempotent second run) |
| deb/release | `make deb`; requirements and signing are in `docs/release.md` |

A plain green `go test ./...` does not prove that the external validators ran:
without the binaries the sing-box/Mihomo checks are skipped; read the output.
Mihomo may download geodata. The Docker bench builds binaries and starts
containers; do not run it on real nodes instead of a test environment.

Useful commands:

```sh
make proto                                      # requires protoc and the Go plugins
make agent GOOS=linux GOARCH=amd64               # bin/agent-linux-amd64
make sing-box GOOS=linux GOARCH=amd64            # bin/sing-box-linux-amd64
# Only for an intentional format change, then check the diff:
go test ./internal/service/singbox -update
go test ./internal/service/subscription -update
```

## Definition of done

1. The cause is fixed and the architectural boundaries are kept; the diff is limited to the task.
2. Related migrations, contracts, configuration and documentation are updated.
3. The necessary checks are done; for code, `make build test lint` and checks of the
   affected behavior. For documentation, verification of facts, paths, commands and the diff.
4. The report states the result, the checks performed and significant limitations.
   A skipped check or a historical successful run is not presented as a current success.
