# iduna-vultr: provision IDUNA on Vultr

GCP was shut down for billing on 2026-11-26, and IDUNA went with it: the GKE pod, the PVC and the database are all
gone. This tool brings IDUNA back as a **fresh start** on one Vultr VM. It is the sibling of WOTAN's
`wotan-vultr` (`WOTAN/ops/vultr`) and has the same guard rails: nothing has a default unless someone confirmed it,
and nothing is bought until you type the price.

```
plan [--dns]             read-only: checks the env and the local files, shows Vultr's own price, spends nothing
apply [--dns]            buys ONE instance (only if IDUNA_CONFIRM_MONTHLY_USD equals the plan's price), pushes the
                         secrets in over SSH, starts IDUNA; --dns then points the names at it (old records saved first)
status --instance ID     state, IPs
destroy --instance ID    deletes it (needs IDUNA_CONFIRM_DESTROY=ID); --restore-dns puts DNS back first
```

**Status: live since 2026-11-26.** Box `iduna` in `ord` (vc2-1c-2gb, $10/month, `137.220.58.252`, instance
`4d57441b-1ab0-4b71-9f9a-b651c2ca9252`), image `ghcr.io/emilyspringerton/iduna:5243d7b`. `iam.okemily.com`,
`okemily.com` and `www` are proxied to it. The sc-tunnel pin is
`a8d28b48827066e2068163b5660fb83d1e5b0a50e4876b9ff088dc643edd7b71`.

## What the box is

One service per box: the founder's call on 2026-11-26. WOTAN gets its own box from `wotan-vultr`, with
`WOTAN_IDUNA_UPSTREAM=https://iam.okemily.com`.

| | |
|---|---|
| OS | Ubuntu (checked before buying). `docker.io` and `docker-compose-v2` come from Ubuntu's own archive. |
| `iduna` | `IDUNA_IMAGE` from GHCR, pinned to a commit tag (`:latest` is refused). Listens on `127.0.0.1:8080`. State lives in `/srv/iduna/var` (= `/app/var`). |
| `web` | nginx serving `/srv/iduna/var/www`: the okemily.com site, made of the OKEMILY repo rsynced in by `okemily-deploy` (same exclusions as the monorepo's `okemily-deploy.sh`) plus the blog, tyler and prompt-o-verse output that IDUNA renders itself. |
| `sc-tunnel` | PARENA secure-channel server, public `:8443` → `iduna:8080`. Built on the box from `EDGE.GAME/tools/sc_tunnel.c`. Its only previous build was the `sc-tunnel:v3` image in Artifact Registry, which is gone. |
| `logsync` | `EMILY/gitops/images/dwlogs/logsync.sh` (copied here). It mirrors DEADWEIGHT's match and deck logs for WOTAN's stats. **Until `IDUNA_DWLOGS_SRC` points at a running DEADWEIGHT, it logs an error every 30 s.** |
| host nginx | `iam.<zone>`, `<zone>` and `www.<zone>`, Cloudflare-proxied, using a Cloudflare Origin CA cert. The routes are the GKE Gateway's: `iam` `/` redirects to the SSO login; on the apex, IDUNA's paths go to IDUNA and everything else goes to the site. `/gfd-ws` returns 503 until GFD is back. WebSockets get 3600 s timeouts. |

**Secrets never go in user-data.** Vultr keeps user-data readable from inside the instance, and this box runs NOCK,
which compiles user-submitted code. `apply` waits for cloud-init to finish, then writes each secret straight from
stdin over SSH with its final mode:

- origin cert and key
- `webmaster.json`
- optionally the `IDUNA_ENV_FILE`

Only after that does it run `iduna-up`.

**First admin:** IDUNA seeds uid 0 from `var/webmaster.json` (email, password, display_name) when it first boots.
`iduna-up` shreds the file as soon as IDUNA reports healthy.

### Changes from the GKE pod

- **Fresh database and fresh ES256 signing key.** Every old JWT and every JWKS consumer's cache is invalid.
- **New `sc-tunnel` key, so a new pin.** `iduna-up` prints the pin. Box-side tunnel clients need it.
- **`NOCK_DATA_DIR` and `NOCK_VIDEO_DIR` are set to paths under `/app/var`.** Their defaults
  (`/home/fatbaby/IDUNA/var/...`) sit outside the volume, so that data would have died with every new container.
  This was found by booting IDUNA fresh before writing the tool.
- **No agents are seeded by the tool.** The image doesn't include `cmd/bootstrap` or `cmd/create-admin-agent`.
  The Back Office agent **HOUSE** (human_operator, `iduna.admin`) was created by hand on 2026-11-26:
  ```bash
  (cd IDUNA && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o create-admin-agent ./cmd/create-admin-agent)
  scp create-admin-agent root@<ip>:/usr/local/bin/
  ssh root@<ip> 'SQLITE_PATH=/srv/iduna/var/iduna.db create-admin-agent -name HOUSE' > ~/secrets/iduna-house-agent.env
  ```
  Its secret is shown once, so redirect it straight into a mode-600 file and never into a terminal or log. It signs in at
  `https://iam.<zone>/admin/login`. M2M agents (EMILY-PRIME, …) still have to be created from `/admin/agents`.
- **`console.okemily.com` isn't served** (not asked for), and **neither is anything that depended on in-cluster
  services:** gfd-core, deadweight, the REDGARDEN pools, dw2_server.

## Environment

| Variable | |
|---|---|
| `IDUNA_VULTR_REGION`, `IDUNA_VULTR_PLAN`, `IDUNA_VULTR_OS_ID` | Required. Leave them unset and run `plan` to get the real choices: 2 GB+ plans with prices, regions, Ubuntu image ids. |
| `IDUNA_ZONE` | Required, e.g. `okemily.com`. The box serves `iam.`, the apex and `www.` of this zone. |
| `IDUNA_IMAGE` | Required, e.g. `ghcr.io/emilyspringerton/iduna:5243d7b` (public; one tag per main commit whose release run passed). |
| `IDUNA_VULTR_SSH_KEY_IDS` + `IDUNA_SSH_KEY_FILE` | Required. A Vultr SSH key id and the matching local private key. Secrets go in over this key. |
| `IDUNA_WEBMASTER_FILE` | Required. A local `{"email","password","display_name"}`, with a password of 12+ characters. Keep it outside every repo. |
| `IDUNA_CONFIRM_MONTHLY_USD` | `apply` only. The plan's price, typed by the person approving the spend. |
| `VULTR_API_KEY_FILE` / `CLOUDFLARE_API_TOKEN_FILE` | Files holding only the key or token. The token needs SSL and Certificates Edit, DNS Edit, and Zone Settings Read. |
| `IDUNA_ORIGIN_OUT_DIR` *or* `IDUNA_ORIGIN_CERT_FILE` + `IDUNA_ORIGIN_KEY_FILE` | Mint an origin cert (saved there with mode 600), or bring one that covers the three hosts. |
| `IDUNA_ROLLBACK_DIR` | `--dns` only. Gets one rollback file per name, and must be empty. |
| `IDUNA_TUNNEL_HOST` | Optional, e.g. `tunnel.okemily.com`. This record is DNS-only, because Cloudflare's proxy carries HTTP, not raw TCP. Unset means you reach the tunnel by the box's IP. |
| `IDUNA_ENV_FILE` | Optional. Extra IDUNA env (`GOOGLE_CLIENT_ID`, `REDGARDEN_TICKET_SECRET`, …), installed as `/etc/iduna/iduna.env`. |
| `IDUNA_DWLOGS_SRC` | Optional. DEADWEIGHT's logserve, `http(s)://host:port`. |
| `IDUNA_EDGE_GIT_URL`/`_REF`, `IDUNA_OKEMILY_GIT_URL`/`_REF` | Default to the public GitHub repos at `main`. |

## Run

```bash
cd IDUNA/ops/vultr
export GOWORK=off
go run . plan                                        # with nothing set: shows the plan/region/OS choices
export IDUNA_VULTR_REGION=... IDUNA_VULTR_PLAN=... IDUNA_VULTR_OS_ID=... IDUNA_ZONE=okemily.com \
       IDUNA_IMAGE=ghcr.io/emilyspringerton/iduna:5243d7b IDUNA_VULTR_SSH_KEY_IDS=... IDUNA_SSH_KEY_FILE=~/.ssh/id_ed25519 \
       IDUNA_WEBMASTER_FILE=~/secrets/webmaster.json VULTR_API_KEY_FILE=... CLOUDFLARE_API_TOKEN_FILE=... \
       IDUNA_ORIGIN_OUT_DIR=~/secrets/iduna-origin IDUNA_ROLLBACK_DIR=~/secrets/iduna-dns-rollback
go run . plan --dns                                  # price, TLS source, and the records it would replace
IDUNA_CONFIRM_MONTHLY_USD=<price from plan> go run . apply --dns
```

The order of `apply`:

1. Mint the origin cert (free).
2. Create the instance. This is the only paid call; the tool prints the instance id and the undo command.
3. Wait for SSH and cloud-init.
4. Push the secrets.
5. Run `iduna-up`: build sc-tunnel, sync OKEMILY, pull and start, wait for `/health`, shred the seed, print the tunnel pin.
6. Save the old records, then point the names.
7. Wait for `https://iam.<zone>/health` to return 200 through Cloudflare, with TLS verified.

**Today** (checked 2026-11-26), `okemily.com`, `www` and `iam` are proxied records that return Cloudflare's 525,
because their GKE origin is dead. Replacing them takes down nothing that currently works.

Undo:

```bash
IDUNA_CONFIRM_DESTROY=<id> go run . destroy --instance <id> --restore-dns
```

That **deletes IDUNA's database.** Back up `/srv/iduna/var` first if anything in it matters.

Update in place (a new IDUNA image): `ssh root@<ip>`, edit `IDUNA_IMAGE` in `/etc/iduna/box.env` and `image:` in
`/opt/iduna/compose.yml`, then run `iduna-up`.

## Tests

`go test ./...` runs against fake Vultr and Cloudflare APIs and never touches the real ones. It covers:

- **Config:** required env, and rejection of injection attempts and `:latest`.
- **Price:** the guard.
- **User-data:** it carries no secrets, and it and every script it writes pass `bash -n`.
- **Local files:** the webmaster/SSH key checks.
- **Create payload:** what is sent to Vultr.
- **DNS:** a point/restore round trip over four names, with the tunnel record DNS-only.

Also checked by hand on 2026-11-26:

- The rendered compose file parses as YAML.
- The rendered nginx site passes `nginx -t` on Ubuntu 24.04's nginx 1.24.0.
- `sc_tunnel` built from EDGE.GAME carried HTTP end to end with a freshly generated key.
- IDUNA booted from an empty `var/` (SQLite, fresh ES256 key, `/` and JWKS 200), once the NOCK paths were set.
