# giocondo-vpn

[![CI](https://github.com/lavr-art/giocondo/actions/workflows/ci.yml/badge.svg)](https://github.com/lavr-art/giocondo/actions/workflows/ci.yml)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26%2B-00ADD8.svg)](go.mod)

A self-hosted control panel for personal [sing-box](https://sing-box.sagernet.org/) VPN nodes.

The panel stores all state: users, terms, traffic limits, WireGuard keys, nodes and traffic history.
A small agent on every node receives its config from the panel and runs sing-box. Users get one
subscription link that works in Hiddify, Happ, v2rayNG, Karing, sing-box and Mihomo/Clash clients.

## Features

- **Protocols**: VLESS Reality, Hysteria2 and WireGuard, all served by sing-box.
- **Single source of truth**: a lost node is restored from the panel; the agent has no database of its own.
- **Secure node link**: the agent opens an outbound gRPC stream to the panel with mTLS and a private CA.
  No management port is opened on nodes, and sing-box keeps working with the last config if the panel goes away.
- **Safe config rollout**: every config is validated with `sing-box check`, applied atomically and rolled back on failure.
  Changes are throttled, so a burst of edits causes one restart.
- **Traffic accounting**: per-user counters via the v2ray stats API (including WireGuard), with deduplication
  so traffic is neither lost nor counted twice on reconnects and restarts.
- **Users**: terms, traffic limits, limit resets that keep history, extensions, optional auto-deletion of expired users.
- **Subscriptions**: base64 links, sing-box JSON (1.12+) or Mihomo YAML, picked by `User-Agent` or `?format=`,
  with `subscription-userinfo` and profile headers. Host overrides let you publish nodes under other addresses.
- **Routing**: private networks and Russian zones/categories go direct, blocked resources and everything else go
  through the VPN, based on [runetfreedom/russia-v2ray-rules-dat](https://github.com/runetfreedom/russia-v2ray-rules-dat).
- **Web UI and REST API**: `html/template` + htmx with a strict CSP, a secret admin path, and an OpenAPI-described
  API with scoped tokens.
- **Node provisioning**: install a node over SSH or with a cloud-init snippet; optional Cloudflare DNS records for nodes.
- **Health checks**: real client connections to every node, with an optional external probe point.
- **Webhooks** for panel events.
- **Easy to run**: a single Go binary, SQLite without CGO, deb packages for amd64/arm64 from a signed apt repository.

## Architecture

```
                    admin (browser / REST)          VPN clients
                            │                     │          │
                            ▼                     │ /sub/…   │ VLESS / Hysteria2 / WireGuard
   ┌───────────────────────────────────┐          │          │
   │ panel  admin UI + API   :8080     │◄─────────┘          │
   │        subscriptions    :8081     │                     │
   │        gRPC Sync        :9443     │                     │
   │        SQLite, PKI                │                     ▼
   └───────────────▲───────────────────┘       ┌────────────────────────────┐
                   │  gRPC Sync over mTLS       │ node                       │
                   └────────────────────────────┤  giocondo-vpn-agent        │
                     (opened by the agent)      │  sing-box (with v2ray_api) │
                                                └────────────────────────────┘
```

The panel builds each node's desired config from its database, the agent applies it and reports
the applied hash, health and traffic deltas back over the same stream.

## Quick start

Install the panel and a node on one clean Debian 11+ / Ubuntu 22.04+ server (amd64 or arm64), as root:

```sh
# By IP: HTTPS with a Let's Encrypt IP certificate at https://<IP>:8443 (port 80 must be reachable)
bash <(curl -fsSL https://lavr-art.github.io/giocondo/install.sh)

# With a domain: HTTPS through Caddy and Let's Encrypt at https://vpn.example.com:8443
bash <(curl -fsSL https://lavr-art.github.io/giocondo/install.sh) --domain vpn.example.com

# Plain HTTP by IP on :8081, when port 80 cannot be opened
bash <(curl -fsSL https://lavr-art.github.io/giocondo/install.sh) --http
```

The IP certificate lives about 6 days and Caddy renews it, so port 80/tcp must stay open. If the certificate
is not issued, the installation fails and is rolled back.

The installer is idempotent: running it again changes nothing that is already in place.

Open the admin UI:

- **without a domain** (by IP or `--http`): `ssh -L 8080:127.0.0.1:8080 root@<IP>`, then `http://127.0.0.1:8080<PANEL_ADMIN_PATH>/`;
- **with a domain**: `https://<domain>:8443<PANEL_ADMIN_PATH>/`.

The admin path and password are generated on install:

```sh
grep -E '^PANEL_ADMIN_(PATH|PASSWORD)=' /etc/giocondo-vpn/panel.env
```

Other installer commands and options:

```sh
install.sh status
install.sh uninstall [--purge]
install.sh --ip A            # public IPv4 when the default route has a private address
install.sh --http            # plain HTTP subscriptions instead of HTTPS by IP
install.sh --version V       # pin the package version
install.sh --reconfigure --domain D | --ip A | --http   # change the public address or mode
install.sh --yes             # non-interactive
```

Ports opened on the server:

| Port | Purpose |
| --- | --- |
| 443/tcp | VLESS Reality |
| 8443/udp | Hysteria2 |
| 51820/udp | WireGuard |
| 8081/tcp | Subscriptions (`--http`) |
| 80/tcp, 8443/tcp | Caddy: ACME and HTTPS for subscriptions (by IP) or the UI and subscriptions (with a domain) |

See [docs/release.md](docs/release.md#quick-install-on-a-clean-server) for the full installer behavior.

## Adding more nodes

By default the panel gRPC listens on `127.0.0.1:9443`, which is enough for the local node. For remote nodes:

1. Set `PANEL_GRPC_LISTEN=:9443` (and `PANEL_AGENT_ADDR` if needed) in `/etc/giocondo-vpn/panel.env`;
   the panel certificate must cover the public address.
2. Add a node in the admin UI and install it over SSH or with the generated cloud-init snippet.
   The same is available from the CLI: `giocondo-vpn-panel provision ssh|cloud-init -node NAME`.

A node can also be set up by hand from the apt packages `giocondo-vpn-agent` and `giocondo-vpn-sing-box`,
see [docs/release.md](docs/release.md#manual-node-setup-without-install-agentsh).

## Configuration

Settings are environment variables in the systemd `EnvironmentFile`s. Package upgrades never overwrite them.

| File | Purpose |
| --- | --- |
| `/etc/giocondo-vpn/panel.env` | Panel settings ([template](deploy/apt/deb/panel.env)) |
| `/etc/giocondo-vpn/agent.env` | Agent settings ([template](deploy/apt/deb/agent.env)) |
| `/var/lib/giocondo-vpn/panel/` | Panel database and PKI |
| `/etc/giocondo-vpn/sing-box/config.json` | Current node config, managed by the agent |

Main panel settings:

| Variable | Default | Description |
| --- | --- | --- |
| `PANEL_PUBLIC_URL` | | Public address of the panel |
| `PANEL_SUB_URL` | `PANEL_PUBLIC_URL` | Public address of subscriptions |
| `PANEL_ADMIN_PATH` | random | Secret path prefix for the admin UI and API |
| `PANEL_ADMIN_PASSWORD` | random | Admin UI password |
| `PANEL_LISTEN` | `127.0.0.1:8080` | Admin UI and API listener |
| `PANEL_SUB_LISTEN` | `127.0.0.1:8081` | Subscription listener |
| `PANEL_GRPC_LISTEN` | `:9443` | gRPC listener for agents |
| `PANEL_WEBHOOK_URL`, `PANEL_WEBHOOK_SECRET` | | Event webhooks |
| `PANEL_AUTODELETE_EXPIRED_DAYS` | `0` | Delete users expired more than N days ago |
| `PANEL_DNS_DOMAIN`, `PANEL_CLOUDFLARE_*` | | Managed node DNS records in Cloudflare |
| `PANEL_HEALTH_INTERVAL` | `5m` | Node availability check interval |
| `PANEL_PROBE_URL`, `PANEL_PROBE_TOKEN` | | Optional external probe point |

Agent settings: `AGENT_PANEL_ADDR` (`host:port`, comma-separated fallbacks), `AGENT_PANEL_SERVER_NAME`,
`AGENT_LOG_LEVEL`, `AGENT_STATS_INTERVAL`.

## Subscriptions

```
GET /sub/<token>[?format=links|singbox|clash]
```

`format` takes precedence over the `User-Agent`. Hiddify gets base64 links, Karing and sing-box clients get JSON,
Mihomo-compatible clients get YAML. An unknown token returns `404`, a disabled user `403`; an expired user or
one over the limit still gets `200` with the real `subscription-userinfo`, but is removed from node configs.

## REST API

The API lives under the admin path and is described in [docs/api.yaml](docs/api.yaml) (OpenAPI).
It uses Bearer tokens `vpc_…` with scopes `monitor` ⊂ `node-sync` ⊂ `admin`. Only a hash is stored,
and the raw token is shown once:

```sh
giocondo-vpn-panel token create -name grafana -scope monitor
giocondo-vpn-panel token list
```

Tokens can also be managed in the admin UI.

## Building from source

Requirements: Go 1.26+ and `golangci-lint` for linting; `protoc` with the Go plugins only for `make proto`.

```sh
make build test lint                     # bin/panel, bin/agent; go test ./...; golangci-lint
make sing-box                            # our sing-box build with with_v2ray_api -> bin/sing-box
make agent GOOS=linux GOARCH=amd64       # bin/agent-linux-amd64
make sing-box GOOS=linux GOARCH=arm64    # bin/sing-box-linux-arm64
make deb                                 # snapshot deb packages in dist/ via GoReleaser
make proto                               # regenerate gRPC code from proto/
```

Repository layout:

| Path | Contents |
| --- | --- |
| `cmd/panel`, `cmd/agent` | Panel and agent entry points, panel CLI |
| `internal/admin` | Business operations shared by the web UI and the API |
| `internal/api`, `internal/web` | REST API and web UI |
| `internal/store` | SQLite storage and migrations |
| `internal/nodehub`, `internal/agent` | Both sides of the Sync link |
| `internal/singbox`, `internal/subscription` | Server config and client subscription generators |
| `internal/provision`, `internal/dns`, `internal/healthcheck`, `internal/webhook` | Provisioning, DNS, checks, events |
| `proto/` | gRPC contracts |
| `deploy/apt/` | deb scripts, systemd units, agent installer, apt repository script |
| `install.sh` | One-line server installer (panel + local node) |
| `build/sing-box/` | Separate Go module pinning the sing-box version |
| `test/network-e2e/` | Docker network test bench |

## Testing

```sh
go test ./...
go test -race ./...
```

Some tests validate generated configs with external binaries and are skipped when they are missing:

```sh
make sing-box && PATH="$PWD/bin:$PATH" go test -count=1 ./internal/singbox
MIHOMO_CHECK=1 go test -count=1 ./internal/subscription   # requires mihomo in PATH
```

End-to-end network tests run in an isolated Docker bench (never on real nodes):

```sh
sh test/network-e2e/run.sh              # DNS/IP operations, node availability
sh test/network-e2e/run.sh installer    # install.sh on a clean container
```

## Releases

Pushing a `v*` tag builds deb packages and a GitHub Release with GoReleaser and publishes them to the signed
apt repository on GitHub Pages. See [docs/release.md](docs/release.md).

## Scope

giocondo-vpn is built for personal and small-group use. Not implemented: PostgreSQL, creating VMs through
cloud provider APIs, a Telegram bot, AmneziaWG.

## Contributing

Issues and pull requests are welcome. Before sending a PR, run `make build test lint` and the extra checks
from [Testing](#testing) that match your change. Commit messages follow `type(area): short action`.

## Security

Please report vulnerabilities privately through
[GitHub security advisories](https://github.com/lavr-art/giocondo/security/advisories/new)
instead of public issues, and never include real tokens, keys or subscription links.

## License

[GPL-3.0](LICENSE)
