# Release and apt repository

Nodes install the agent as deb packages from the project apt repository on GitHub Pages:
`vpnctl-agent` (agent, unit, `/etc/vpnctl/agent.env` template) and `vpnctl-sing-box` (our own sing-box
build with `with_v2ray_api` in `/usr/lib/vpnctl/sing-box`, unit `vpnctl-sing-box.service`).

## One-time setup

1. A signing key (no passphrase, used only for the repository):

   ```
   gpg --batch --passphrase '' --quick-gen-key 'vpnctl apt <apt@vpnctl.invalid>' ed25519 sign never
   gpg --armor --export-secret-keys 'vpnctl apt' > apt-signing.asc   # into the secret, then delete the file
   ```

2. GitHub → Settings → Secrets → Actions: `APT_GPG_PRIVATE_KEY` = contents of `apt-signing.asc`.
3. After the first release (the `gh-pages` branch appears by itself): Settings → Pages → Deploy from a branch →
   `gh-pages`, `/ (root)`. The repository address is `https://<user>.github.io/<repo>`; if it is not
   `https://artemlavr.github.io/giocondo`, set it for the panel in `PANEL_APT_REPO`.

## Release

```
git tag v0.1.0 && git push origin v0.1.0
```

`.github/workflows/release.yml`: GoReleaser (`.goreleaser.yaml`) builds the packages and the GitHub Release,
then `deploy/apt/update-repo.sh` adds them to `gh-pages` and signs `Release`/`InRelease`.
Old versions stay in `pool/`.

Locally: `make deb` (snapshot in `dist/`, requires at least one commit).

## Updating sing-box

```
cd build/sing-box && go get -tool github.com/sagernet/sing-box/cmd/sing-box@vX.Y.Z && go mod tidy
```

Build tags live in the `Makefile` and `.goreleaser.yaml`; keep them identical.

## Manual node setup (without install-agent.sh)

```
curl -fsSL https://artemlavr.github.io/giocondo/vpnctl.gpg -o /usr/share/keyrings/vpnctl.gpg
echo 'deb [signed-by=/usr/share/keyrings/vpnctl.gpg] https://artemlavr.github.io/giocondo stable main' \
  > /etc/apt/sources.list.d/vpnctl.list
apt-get update && apt-get install -y vpnctl-agent
```

Then put the node certificates into `/etc/vpnctl/agent/{ca.crt,agent.crt,agent.key}`, set `AGENT_PANEL_ADDR` in
`/etc/vpnctl/agent.env` and run `systemctl restart vpnctl-agent`. Updates: `apt-get upgrade`.

## Panel from apt

```
# the repository is added the same way as for a node (see above)
apt-get install -y vpnctl-panel        # pulls vpnctl-agent and vpnctl-sing-box via Recommends
```

postinst creates the `vpnctl` user, `/etc/vpnctl/panel.env` with random `PANEL_ADMIN_PATH`,
`PANEL_ADMIN_PASSWORD`, `PANEL_SESSION_SECRET` (and prints them) and a CA in `/var/lib/vpnctl/panel/pki`. Next:

```
# 1. PANEL_PUBLIC_URL in /etc/vpnctl/panel.env, a reverse proxy with HTTPS to 127.0.0.1:8080 and :8081
sudo -u vpnctl vpnctl-panel ca server -dir /var/lib/vpnctl/panel/pki -host panel.example.com,127.0.0.1
systemctl start vpnctl-panel
sudo -u vpnctl vpnctl-panel token create -name me -scope admin   # REST API token
sudo vpnctl-panel local-node -host vpn.example.com               # the same machine as a node
```

The admin UI is at `https://panel.example.com<PANEL_ADMIN_PATH>/`, the API under the same path. Removal with
`purge` deletes `panel.env` but not `/var/lib/vpnctl/panel` (the database and the node CA).
