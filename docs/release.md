# Release and apt repository

Nodes install the agent as deb packages from the project apt repository on GitHub Pages:
`giocondo-vpn-agent` (agent, unit, `/etc/giocondo-vpn/agent.env` template) and `giocondo-vpn-sing-box` (our own sing-box
build with `with_v2ray_api` in `/usr/lib/giocondo-vpn/sing-box`, unit `giocondo-vpn-sing-box.service`).

## One-time setup

1. A signing key (no passphrase, used only for the repository):

   ```
   gpg --batch --passphrase '' --quick-gen-key 'giocondo-vpn apt <apt@giocondo-vpn.invalid>' ed25519 sign never
   gpg --armor --export-secret-keys 'giocondo-vpn apt' > apt-signing.asc   # into the secret, then delete the file
   ```

2. GitHub → Settings → Secrets → Actions: `APT_GPG_PRIVATE_KEY` = contents of `apt-signing.asc`.
3. After the first release (the `gh-pages` branch appears by itself): Settings → Pages → Deploy from a branch →
   `gh-pages`, `/ (root)`. The repository address is `https://<user>.github.io/<repo>`; if it is not
   `https://lavr-art.github.io/giocondo`, set it for the panel in `PANEL_APT_REPO`.

## Release

```
git tag v0.1.0 && git push origin v0.1.0
```

`.github/workflows/release.yml` first runs source checks, then GoReleaser (`.goreleaser.yaml`) builds unpublished
candidates. Real package install, previous-stable upgrade and recovery tests run on amd64 and arm64.
Only the tested artifacts are uploaded to a draft GitHub Release; `deploy/apt/update-repo.sh` adds them to
`gh-pages` and signs `Release`/`InRelease`, and the release is then made public.
Old versions stay in `pool/`.

See [controlled upgrades and recovery](upgrades.md) for the supported version route and backup/restore procedure.

Locally: `make deb` (snapshot in `dist/`, requires at least one commit).

## Updating sing-box

```
cd build/sing-box && go get -tool github.com/sagernet/sing-box/cmd/sing-box@vX.Y.Z && go mod tidy
```

Build tags live in the `Makefile` and `.goreleaser.yaml`; keep them identical.

## Manual node setup (without install-agent.sh)

```
curl -fsSL https://lavr-art.github.io/giocondo/giocondo-vpn.gpg -o /usr/share/keyrings/giocondo-vpn.gpg
echo 'deb [signed-by=/usr/share/keyrings/giocondo-vpn.gpg] https://lavr-art.github.io/giocondo stable main' \
  > /etc/apt/sources.list.d/giocondo-vpn.list
apt-get update && apt-get install -y giocondo-vpn-agent
```

Then put the node certificates into `/etc/giocondo-vpn/agent/{ca.crt,agent.crt,agent.key}`, set `AGENT_PANEL_ADDR` in
`/etc/giocondo-vpn/agent.env` and run `systemctl restart giocondo-vpn-agent`. Updates: `apt-get upgrade`.

## Quick install on a clean server

`install.sh` is published as `https://lavr-art.github.io/giocondo/install.sh` with every release. It installs
the panel and a node on the same Debian 11+/Ubuntu 22.04+ server (amd64/arm64) and can be run again safely: every step
checks the current state. An unchanged repeat run preserves secrets, certificates and network settings and does not
restart healthy services. Package upgrades may restart services through their package scripts.

```
bash <(curl -fsSL https://lavr-art.github.io/giocondo/install.sh)                       # HTTPS by IP
bash <(curl -fsSL https://lavr-art.github.io/giocondo/install.sh) --domain vpn.example.com # HTTPS by domain
bash <(curl -fsSL https://lavr-art.github.io/giocondo/install.sh) --http                  # plain HTTP by IP
```

- Without a domain Caddy serves subscriptions at `https://<IP>:8443` with a Let's Encrypt IP certificate (shortlived
  profile, about 6 days, renewed by Caddy). Port 80/tcp must be reachable from the internet for issuance and every
  renewal. Caddy 2.10.2 or newer is required; distribution packages are older, so the official Caddy repository is
  used. Only `/sub/*` and `/health/*` are proxied: the admin UI listens on `127.0.0.1:8080` only, open it with
  `ssh -L 8080:127.0.0.1:8080 root@<IP>`. If the certificate is not issued within two minutes, the installation fails
  and is rolled back; use `--http` where port 80 cannot be opened.
- With `--http` subscriptions are plain HTTP at `http://<IP>:8081` (the token in the link is visible on the way);
  the admin UI is local as above.
- With `--domain` Caddy serves both at `https://<domain>:8443` with a Let's Encrypt certificate; port 80 must be reachable.
- Port 443 stays with VLESS Reality, so Caddy uses only the HTTP-01 challenge for IP certificates. Caddy uses HTTP/1.1
  and HTTP/2; HTTP/3 is disabled to leave UDP 8443 for Hysteria2.
- The panel gRPC listens on `127.0.0.1:9443`: enough for the local node. Remote nodes need `PANEL_GRPC_LISTEN=:9443`
  and a panel certificate that covers the public address.
- Other options: `--ip A` (when the default route has a private address), `--version V`, `--repo URL`, `--yes`;
  commands `status` and `uninstall [--purge]`.
- The mode, HTTP/HTTPS URLs, listeners, gRPC and VPN ports of an existing installation are preserved when no flags are
  given; an installation made before HTTPS by IP stays HTTP. Conflicting explicit addresses or modes are refused.
  To change the public panel/subscription address or mode, use `--reconfigure --domain D`, `--reconfigure --ip A`
  (HTTPS by IP) or `--reconfigure --http [--ip A]`; existing gRPC settings and client keys remain unchanged.
  Switching to `--http` leaves an existing Caddy configuration in place.
- Caddyfile replacement requires an unchanged package default or the saved checksum from this installer. Custom,
  edited and legacy marker-only files are refused before panel settings change, with a site block for manual integration.
  Validation/reload failures restore the previous configuration. `--yes` does not bypass this protection.
- The installer prepares the CA bundle before HTTPS downloads, regardless of whether curl/wget already exists.
- Success requires active panel/agent/sing-box services, a valid sing-box config and a fresh local agent heartbeat with
  matching desired, applied and on-disk config hashes. `giocondo-vpn-panel local-node -check` waits up to 120 seconds
  and reads the existing database without migrations. `-ports` reports the local node's actual firewall ports.
  In HTTPS-by-IP mode it also requires a certificate for the IP that the system CAs trust (checked locally with
  `curl --connect-to`). This verifies local readiness, not public reachability of the VPN ports or ACME issuance for
  `--domain`. Incompatible pinned package
  versions are refused before configuration changes. Invalid/partial agent credentials require explicit repair.
- Failure on an existing installation restores the previous panel/Caddy configuration. If a first installation has
  already created the local node credentials, it keeps the matching initial panel settings/certificates for a safe retry.
- The admin path and password are in `/etc/giocondo-vpn/panel.env`; the script does not print them.

After `make deb`, the check is `sh test/network-e2e/run.sh installer`: actual release packages in a clean container,
client traffic over VLESS, Hysteria2 and WireGuard,
an unchanged second run, recovery of a stopped agent (all in `--http` mode), and the generated domain and IP Caddy
HTTPS proxies with a verified local certificate, including that the IP site does not expose the admin UI.
Public ACME issuance by domain or IP needs a real reachable server and is checked separately.
File preservation, reconfiguration, rollback and CA bootstrap regressions run with
`go test -count=1 -v ./test/installer` (included in the ordinary Go suite and CI);
Debian package-hook regressions run on Linux with GNU tar/coreutils and `dpkg`.
Run `shellcheck install.sh` for shell lint. Network scenarios are opt-in Go tests
under the `e2e` build tag, launched by `run.sh`; the bench has no Python dependency.

## Panel from apt

```
# the repository is added the same way as for a node (see above)
apt-get install -y giocondo-vpn-panel        # pulls giocondo-vpn-agent and giocondo-vpn-sing-box via Recommends
```

postinst creates the `giocondo-vpn` user, `/etc/giocondo-vpn/panel.env` with random `PANEL_ADMIN_PATH`,
`PANEL_ADMIN_PASSWORD`, `PANEL_SESSION_SECRET` (and prints them) and a CA in `/var/lib/giocondo-vpn/panel/pki`. Next:

```
# 1. PANEL_PUBLIC_URL in /etc/giocondo-vpn/panel.env, a reverse proxy with HTTPS to 127.0.0.1:8080 and :8081
sudo -u giocondo-vpn giocondo-vpn-panel ca server -dir /var/lib/giocondo-vpn/panel/pki -host panel.example.com,127.0.0.1
systemctl start giocondo-vpn-panel
sudo -u giocondo-vpn giocondo-vpn-panel token create -name me -scope admin   # REST API token
sudo giocondo-vpn-panel local-node -host vpn.example.com               # the same machine as a node
```

The admin UI is at `https://panel.example.com<PANEL_ADMIN_PATH>/`, the API under the same path. Removal with
`purge` deletes `panel.env` but not `/var/lib/giocondo-vpn/panel` (the database and the node CA).
