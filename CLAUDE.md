# IDUNA — Platform IAM & Governance Service

Central trust authority for EINHORN_INDUSTRIAL. Manages user auth (Google OAuth), M2M agent auth,
ES256 JWTs, RBAC, Apples ledger, HEIMDAL sprint planning, and FCM device tokens.

**All downstream services trust only IDUNA-issued JWTs. Never trust external tokens directly.**

## Listening on

`:8080` inside the pod — all HTTP endpoints below. **Runs in Kubernetes** (GKE, namespace
`emily`, `svc/iduna` ClusterIP on 8080/8443/80 — see root `CLAUDE.md`'s "Kubernetes Operations"
section); there is no ingress, so reach it from outside the cluster with
`kubectl port-forward -n emily svc/iduna 18080:8080` and point `IDUNA_BASE_URL` at
`http://localhost:18080`. The local `var/agent-secrets.env` in this repo is a bootstrap artifact
and drifts from the live cluster — get the current value for any agent secret from the running
pod itself: `kubectl exec -n emily deploy/iduna -- cat /app/var/agent-secrets.env`.

## Key Endpoints

| Method | Path | Description |
|---|---|---|
| POST | `/api/v1/auth/google` | Google OAuth callback → IDUNA JWT |
| GET | `/api/v1/auth/sso/login` | IDUNA-as-SSO login page (`SSOLoginHandler`) — the one place a password is typed for email/password auth; `?redirect_uri=` (allowlisted via `SSO_ALLOWED_REDIRECT_HOSTS`) gets the JWT back as a URL fragment. Also served at the root of the dedicated `iam.okemily.com` domain (see `ops/nginx/iam-okemily.conf`) once its DNS/cert land. |
| GET | `/api/v1/auth/sso/google/start` \| `/callback` | "Sign in with Google" for the SSO login page (`SSOGoogleHandler`, `internal/http/handlers/sso_google.go`) — same `redirect_uri` allowlist as the page, state cookie CSRF check, issues the same player JWT as email login, links to an existing email/password account only on a Google-*verified* email. Button only renders when `GOOGLE_CLIENT_ID`+`GOOGLE_CLIENT_SECRET` are set. Requests the minimal `openid email` scope. **Live as of 2026-10-12** (new OAuth client registered under the existing `okemily` GCP brand, separate from exec.okemily.com's). Each registered host picks its own callback URL (`SSO_GOOGLE_REDIRECT_URIS`, see below) — every one of them must also be listed under the OAuth client's Authorized redirect URIs in Google Cloud Console, a human-only step; until it is, Google returns `redirect_uri_mismatch` for that host. **Found and fixed live, 2026-10-12**: a single fixed redirect_uri broke every real sign-in once the dedicated `iam.okemily.com` login domain went live, because its state/redirect cookies are host-scoped and the fixed callback always landed on a different host (`okemily.com`) than the one that set them — `redirectURIFor(r)` now picks the entry matching the request's own Host for both legs. Also found and fixed the same day: `internal/auth/google/verify.go`'s RSA signature check passed the wrong hash identifier (`0x04`/SHA-224) for a SHA-256 digest, so every real id_token failed `crypto/rsa: input must be hashed message` — invisible in tests because every other call site mocks the `VerifyToken` seam; `internal/auth/google/verify_test.go` now builds a real signed JWT against a local JWKS server to catch this class of bug going forward. |
| GET | `/api/v1/auth/sso/battlenet/start` \| `/callback`, `/api/v1/auth/sso/twitch/start` \| `/callback`, `/api/v1/hs/me/identities[/...]`, `/api/v1/hs/me/upload-keys`, `/api/v1/hs/live/exchange`, `/admin/identities` | **WOTAN identity federation (ID-A, `internal/http/handlers/identity*.go`, `hs_identity.go`, `admin_identities.go`, migration `202610130001`)** — linked accounts per player (`player_identities`: battlenet x N / google / twitch x 1 / iduna_local), Battle.net SSO login+link (scope `openid` only; 404 `provider_not_configured` until `BNET_CLIENT_ID`/`BNET_CLIENT_SECRET`), Twitch SSO login+link (IQ-2 resolved 2026-10-12, founder real-time; no scopes either way — identity-only, never the stream key or email, I8; `TWITCH_CLIENT_ID`/`TWITCH_CLIENT_SECRET`), hashed `wt_` tracker upload keys exchanged for the 12 h upload JWT, ingest BattleTag binding, Battle.net gate (club create + key minting; 30-day grace, `wotan.identity.bypass`), Back Office force-unlink / revoke key / operator-assisted merge. Registering the Battle.net and Twitch apps is a human-only step. |
| POST | `/api/v1/auth/agent` | M2M agent auth (agent_name + agent_secret) → JWT |
| GET | `/.well-known/jwks.json` | Public key set for JWT validation |
| POST | `/api/v1/apples` | File a golden documentation Apple |
| GET | `/api/v1/apples` | List Apples (limit, apple_type, source_repo filters) |
| GET/PATCH | `/api/v1/heimdal/sprints` | HEIMDAL sprint planning (MJOLNIR → Emily Prime) |
| GET/POST | `/api/v1/push-tokens/:agent` | FCM device tokens for MJOLNIR push |
| POST | `/api/v1/intelligence/observations` | Camera observations from MJOLNIR |
| POST | `/api/v1/subscriptions` | Provision Emily+ subscription (requires subscriptions.admin) |
| GET | `/api/v1/subscriptions/me` | Get caller's subscription status (requires JWT) |
| GET/POST/PATCH/DELETE | `/api/v1/kanban/cards[/:id]` | Kanban prioritization layer over EMILY/BACKLOG.md (requires kanban.access). `PATCH .../{id} {"queue":"done"}` is a real, special action, not a literal queue: archives the item's own real BACKLOG.md line (checkbox flipped, relocated to a standing archive section), files a real completion Apple, and removes the card. |
| POST | `/services/collector` | Unified logging backend ingest — Splunk HEC-shaped (`Authorization: Splunk <IDUNA_HEC_TOKEN>`), any JSON event |
| GET | `/services/search/jobs` | Unified logging backend search — Splunk-shaped, synchronous v0 (`?search=type=x source=y q=text&regex=pattern`, requires `logs.read`) |
| GET | `/portal/logs` | Log query UI in the developer portal (same search/regex query, requires `devportal.access` + `logs.read`) |
| GET | `/admin/` | Back Office UI (admin role required) |
| GET | `/admin/kanban` | Kanban board UI (Inbox + 3 columns: Backlog/Priority/Cruise, drag-and-drop; admin role required) |
| GET/POST/PATCH/DELETE | `/admin/qr[/api/codes[/:slug]]` | Dynamic QR code registry (admin role required): every QR image encodes IDUNA's own `/q/:slug` redirect URL, never the destination directly, so retargeting a code (`PATCH`) repoints every already-printed copy with zero reprinting. |
| GET | `/q/:slug` / `/q/:slug.png` | Public (no auth) — the real redirect a phone camera hits, and the live-rendered QR image itself (embeddable/printable without an admin session). |
| POST | `/admin/login/qr/api/codes` | Public — mints a QR Back Office login code (S583). `/admin/login/qr/api/codes/:token/qr`\|`/status` are also public (the mint/QR/poll trio IS the login path); `/admin/login/qr/approve/:token` (GET+POST) is the real gate, requiring an already-authenticated `iduna.admin` session — the token itself is never a bearer credential. See `internal/http/handlers/admin_qr_login.go`'s header comment. |
| GET | `/play/big_o` \| `/play/brawlpit` | Public account-creation pages (`GameSignupPageHandler`, game-parameterized — neither game has a native client with this UI yet) — drive the generic `internal/games.Registry` guest-register/guest-login/guest-upgrade API, same code path DEADWEIGHT's own native client already uses. |
| POST | `/api/v1/redeem` | **Account-level "Redeem a code"** (founder real-time 2026-10-16: "an affordance under the user profile for redeem a code ... enable founder mode in DEADWEIGHT web ... say what it did"; WOTAN `/redeem/`, `internal/http/handlers/account_redeem.go`, `GameOnlineHandler.ServeAccount`). Body `{code}`; bearer = the generic SSO JWT a signed-in WOTAN visitor holds (or a game player token, for its own game only; agent tokens refused). Finds the game the unspent code belongs to, resolves the player via `ssoGamePlayer` — the exact rule `sso-exchange` uses (a guest linked through `game_sso_links`, else the identity's own `players` row, claimed for the game on first use; a row already scoped to another game gets 403 `other_game`), then spends the code through `applyClaimCode`, the SAME transaction the per-game `POST /api/v1/games/{game}/redeem` (native client) now shares: tickets added, `is_founder` set, balance lifted to 9,999 for a Founder code, tier set. Answers with the facts (`tickets_granted`, `founder`, `was_founder`, `tickets_before`, `tickets`, `game`, `game_name`, `player_id`, `display_name`); the wording ("Founder unlocked", "+10 tickets") lives in the client (WOTAN `js/redeem-api.js`). Errors are `{error, code}`: `invalid_code_format`, `code_unavailable` (never existed and already used are deliberately indistinguishable), `other_game`, `wrong_game_token`, `no_account`, `agent_token`. Rate-limited like the per-game route; a spent code is logged to the unified log (`iduna:games.redeem`, only the code's last block). Tests: `account_redeem_test.go` (incl. a raced double-redeem). |
| GET | `/api/v1/me/entitlements` | WOTAN's account overview: `{games:[{game,name,player_id,display_name,founder,tickets,account_state}], redemptions:[{code,game,game_name,tickets,founder,tier,redeemed_at}]}` for the caller (SSO JWT, or a game token). Read-only — never claims an identity for a game. The public `GET /api/v1/games/{game}/players/{id}/tickets` also gained `founder` (already implied by a 9,999 balance), which the DEADWEIGHT web client's FOUNDER badge reads. |
| GET (WebSocket) | `/api/v1/shankpit460/play` | **shankpit-460 web's human lobby** (founder real-time 2026-10-16; WOTAN `/shankpit-460/`; `internal/shankpit460` + `handlers/shankpit460_play.go`; wire format in `internal/shankpit460/proto.go`). Binary frames. HELLO carries the visitor's IDUNA token (generic SSO JWT -> `ssoGamePlayer`, the same rule sso-exchange and Redeem-a-code use, so the lobby name is the `shankpit460` game-player row's; a `shankpit460` game token also works; no or a refused token = a guest, `Guest NNNN`). QUEUE puts the client in the search; **two searching humans are paired into a free-for-all room** (a lone searcher waits; more may join the countdown; late arrivals until 4:30 of 6:00). The room owns the clock (**first to 16 kills or six minutes**, `SHANKPIT460_KILL_LIMIT` / `_MATCH_SECONDS` / `_COUNTDOWN_SECONDS` override for tests/ops), health + shield (shield soaks first; a head shot with no shield left is x3), kills / deaths, respawns (2.88 s) and 1 s spawn protection. Movement is client-simulated and relayed (speed-limited); a shot is a HIT *claim* checked against the weapon table, rate of fire, range, line of sight on the embedded `v0_shankpit.map` (at the victim's poses of the last ~300 ms) and spawn protection. END carries the standings; the client then requeues. One connection per signed-in identity (a second replaces the first). **Not a fully server-simulated match** -- it stops impossible damage, wall shots and speed hacks, not aimbots/wallhacks. Results are **not persisted yet** (stats pass). Tests: `internal/shankpit460` (20, incl. -race) and `handlers/shankpit460_play_test.go` (real WebSocket); WOTAN's `scripts/shankpit460-net-test.mjs` runs two real wasm engines against the real binary. The `/api/` Gateway route needs no change (the `iduna` backend policy already has a 3600 s timeout). |
| GET | `/api/v1/shankpit460/status` | Public: `{online, searching, playing, rooms[{id, players, phase, seconds_left, top_kills}], kill_limit, minutes}` -- WOTAN shows it under the shankpit-460 page title. |
| GET (WebSocket) | `/api/v1/redgarden/play` | **REDGARDEN web's arena gateway** (founder real-time 2026-10-17: "publish redgarden web ... FULL WOTAN MATCH AND PLAYER TRACKING ... guest play ... iduna sso ... existing bot match infrastructure and bot pool"; WOTAN `/redgarden/play.html`; `internal/redgardenplay` + `handlers/redgarden_play.go`; wire format in `internal/redgardenplay/proto.go`). Binary frames. HELLO carries the visitor's IDUNA token: a REDGARDEN game token (guest-register / guest-login / email-login / sso-exchange on `/api/v1/games/redgarden`) or the generic SSO JWT (`ssoGamePlayer`); a refused token is an error, never a silent guest (the page makes the guest account first). QUEUE (mode 10v10) makes the gateway do what the native client does over UDP: mint the player's connect ticket (same HMAC as `/redgarden/self-ticket`, needs `REDGARDEN_TICKET_SECRET` = the arena's), send `PACKET_FIND_MATCH` to a pool's matchmaker (default pools: `redgarden-rnd-udp:7778` and `redgarden-stable-udp:8778`, override with `REDGARDEN_POOLS="name\|10v10\|host:port\|20;..."`), connect to the arena server it names, and relay: validated orders in (move / cast / attack / buy / sell / pick ...; coordinates, slots and ids are checked, 90/s cap), the arena's own snapshot bodies out at 20 Hz. The 19-bot persistent pool fills the rest of the lobby, so a human match starts in seconds when the pool is idle; a busy pool means waiting for its bots to come back (a match is <= 12 min). **Recording**: the gateway sees the authoritative snapshots, so it records the result itself -- `redgarden_web_matches` (full record + roster JSON) and, when the match counted (>= 20 s live, a win or a loss), `player_game_stats` (`redgarden-arena`), `redgarden_player_matches` and `redgarden_hero_stats`, i.e. everything WOTAN's leaderboard / profile / hero pages already read. Leaving mid-match is `abandoned` (never a win or loss). The prod arena pods have no IDUNA agent credentials, so the arena reports nothing itself. **Teams are split at slot 10 by the arena whatever the lobby size, so the 3v3 queue is not offered** (six heroes on one team). Inert (503, `/status` says `configured:false`) until `REDGARDEN_TICKET_SECRET` is set. Tests: `internal/redgardenplay` (fakes, -race, ticket scheme, command validation, recorder against the real migrations) and `TestAgainstTheRealArena` (`REDGARDEN_BIN_DIR`: the real C matchmaker + arena server + 19 bots); WOTAN's `scripts/redgarden-browser-test.mjs` runs the whole page. |
| GET (WebSocket) | `/api/v1/d2/play` | **D2 web's gateway** (founder real-time 2026-10-17: "now DEADWEIGHT_2 same affordances WOTAN ESPORTS WASM TS PARENA"; `internal/d2play`, `handlers/d2_play.go`). A WebSocket in front of one TCP connection per player to `dw2_server` (`D2_SERVER_ADDR`, default `deadweight2.emily.svc.cluster.local:7800`, run `--no-auth`). Frames are the server's own (`core/protocol.h`, length dropped) plus `0xA1` who-you-are, `0xA2` recorded, `0xAF` error. First frame `AUTH` = a D2 game token (guest-register/guest-login/sso-exchange on `/api/v1/games/d2`) or the generic SSO JWT (resolved like Redeem-a-code); the gateway introduces the player to the server by name, relays only the player's own frames (sized, 60/s), and records the match from the server's frames: `d2_web_matches` (migration `202610170002`: the packed ship, every round break, hull/armor, rating before/after) plus the generic ladder (`game_matches`/`game_player_stats`, game `d2`) through the shared `applyGameMatch` (also behind `match-result`). Bots are real `d2` player rows (`provider deadweight_bot`), two humans rate once, leaving a live match is a counted forfeit, a match the server cut short is `aborted` and unrated. Tested against the real server and bots (`D2_BIN_DIR=... go test ./internal/d2play/`). **If the server is ever given IDUNA agent credentials it would report too: turn this off first.** |
| GET | `/api/v1/d2/status`, `/api/v1/d2/web-matches[/{id}]`, `/api/v1/d2/players/{player_id}` | Public D2 reads: `{configured, online, searching, playing}`; the browser-match feed (`?limit=&player_id=`; `/{id}` adds the ship and the round-break log); rating, rank, record and recent matches. The ladder itself is the generic `GET /api/v1/games/d2/leaderboard`. |
| GET | `/api/v1/redgarden/status` | Public: `{configured, online, searching, playing, pools[{name, mode, lobby, searching, playing, state}]}`. |
| GET | `/api/v1/redgarden/web-matches` | Public feed of matches played through the web client (`?limit=&player_id=`): display name, hero, result, kills/deaths, duration, roster. Wins and losses only. |
| GET | `/admin/kanban/api/inbox` | Real, open (unchecked), not-yet-carded `EMILY/BACKLOG.md` items — the live bridge from the backlog file to the board (admin role required) |
| * | `console.okemily.com/` (host-scoped, `NOCK_CODE_SERVER_HOST`) | VS Code (code-server, unmodified upstream) as a NOCK tool — a plain reverse proxy (`internal/http/handlers/nock_code_proxy.go`) to a local code-server instance (`ops/systemd/nock-code-server.service`, 127.0.0.1:8892, its own auth disabled). Gated by the exact same `RequireCookieAuth`+`iduna.admin` chain as every other admin route — same Back Office login, not a separate auth system. **Corrected 2026-09-21**: originally mounted under a `/admin/nock/code/` subpath — found live (real click-through, not assumed) that code-server's own router has no subpath support at all, so it's a dedicated root-mounted host instead (`ops/nginx/console-okemily.conf` has the full story). Linked from NOCK's own "Code" tab as a plain link, not an iframe. |
| * | `/admin/nock/api/videos[/:id[/original\|proxy\|thumb\|reproxy]]`, `/admin/nock/api/video-upload-links[/:id[/qr]]`, `/admin/nock/api/video-timelines[/:id[/render\|output]]` | **MIXFORGE EDITOR** (NOCK's video editor, `iduna.admin`) — clip library, phone upload links (QR), non-linear timelines (crossfade transitions + fade envelopes, PARENA-wasm-computed via `frontend/nock/src/video/nle.wasm` and matched exactly in ffmpeg's `xfade`/`acrossfade`/`fade` filters on render), an MPC pad-capture panel (grab a clip live off the playing stream, in addition to upload + manual mark-in/mark-out), and a live client-side non-linear preview. `internal/nock/video_store.go`, `handlers/nock_videos.go`. Media on disk under `NOCK_VIDEO_DIR`; needs `ffmpeg`/`ffprobe`. See `docs/NOCK_NORTHSTAR.md`'s "MIXFORGE EDITOR" section. |
| GET/POST | `/nock/upload/:token` | Public (no login — the short-lived link token is the credential) mobile upload page + streaming multipart upload for the NOCK video editor. `NOCK_VIDEO_MAX_MB` caps size; nginx `client_max_body_size` must match. |
| GET/POST/DELETE | `/admin/nock/api/robots[/:id[/spec|/grobot|/gskel]]` | NOCK robot registry (2026-09-27, founder real-time: "upgrade shankpit and nock to formal rigid body physics ... real robot data from industrial data sheets"). Stores GOLDEN BAND datasheet robot rigs (`GOLDENBAND/format/GROBOT_FORMAT.md`): the source-cited `.grobot.json` spec + the compiled physics rig + optional `.gskel`. Upload rejects a rig whose embedded spec hash doesn't match the uploaded spec, or a spec with uncited links/joints (`internal/nock/robot_store.go`). NOCK "Robots" tab shows the per-joint datasheet envelope, sources, and a 3D rig posable within the manufacturer's joint ranges (`iduna.admin`). **Git is the source of truth**: `POST .../robots/sync` (the tab's "Sync from git" button), IDUNA startup, and `go run ./cmd/nock robots-sync` all call `RobotStore.SyncFromGit`, re-importing GOLDENBAND's `robots/*.grobot.json` + `assets/robots/*` from `NOCK_ROBOTS_GIT_DIR` (default `/home/fatbaby/GOLDENBAND`, `off` disables); git-owned rows update in place, hand uploads are never overwritten, the checkout is only read (never pulled). |
| GET | `/admin/openexecutive` | Back Office status + M2M-credential-provisioning page for OpenExecutive (S506) — reuses `/api/v1/openexecutive/provision`'s own core logic (`OpenExecutiveHandler.provisionCore`) so the two can't drift. States plainly that the integration is code-merged/tested but not yet live (needs a real GCP Vertex AI project, a human-only step). Found and fixed a real, previously-undiscovered bug building this: `openexec.read`/`openexec.admin`, the provision endpoint's own documented example permissions, were never actually registered as rows (`migrations/truestore/202609210001_openexecutive_permissions.sql`) — every prior grant of either one had silently failed closed. |
| POST | `/admin/nock/api/shankpit-widgets/import-gltf` | NOCK glTF importer → SHANKPIT Widget bridge (`internal/nock/gltf_widget.go`, `ShankpitWidgetsHandler.importGLTF`): one AABB box wall per glTF mesh node, `door*`-named nodes become scriptless doors, `preview=1` converts without saving. Also the "Import glTF as widget" control in NOCK's Widgets tab. `POST /admin/nock/api/shankpit-levels/import-gltf` is the same conversion for levels (stateless: returns `{walls, doors}`, the level editor merges them into the draft — "glTF scale" file control in the editor toolbar). Both accept a Blender "glTF Separate" export (`file` = the .gltf, repeated `resource` = its .bin) as well as a single .glb; Draco-compressed files are refused with a re-export hint (`internal/nock/gltf_convert.go` `loadGLTFBytesWithResources`, shared by every glTF importer). |
| (convention) | wall named `buggy_spawn` / `buggy_spawn_<deg>` | **Buggy spawns** (#464): a thin wall with that name (NOCK level/widget editors: "+ Add buggy spawn") exports a `buggy_spawns` entry at its top surface (`internal/shankpit/level_store.go` `buggySpawnsForExport`; widget placement rotation is added to the yaw; max 16). SHANKPIT's `level_boxes.h` parses it and `scene_load` spawns the buggy. No schema change. |
| GET | `/api/v1/shankpit-models/MODEL_<NAME>` | **Public, read-only held/world models** (#447). Serves the NOCK widget with that exact name as `{name, walls[]}` — ONLY widgets named `MODEL_*` (every other widget stays admin-only). The hammer: model it in Blender, import via the Widgets tab glTF importer, save as `MODEL_HAMMER`; the SHANKPIT client fetches it at startup (caches `shankpit_model_MODEL_HAMMER.json`, falls back to a built-in hammer) and draws the boxes in hand, 1st and 3rd person. Orientation/scale don't matter: the client puts the longest axis along the hand (+Z), the heavier end forward, scaled to fit. `internal/http/handlers/shankpit_models_public.go`. |
| GET/POST/PATCH/DELETE | `/admin/nock/api/sounds[/:id[/audio]]` + `/admin/nock/api/sound-filters[/:id[/clone]]` | NOCK sound library (upload/record, raw audio body) and named, validated filter chains (2026-09-27). Processing never runs server-side: NOCK's Sounds/Booth tabs run PARENA `stdlib/audio` compiled to TS (`scripts/gen_nock_audio.sh`), SHANKPIT runs the same .prn compiled to C. |
| GET | `/api/v1/nock-sound-filters/:name` | Public, read-only filter-chain lookup by name for engines with no IDUNA login (SHANKPIT). |
| GET | `/health` | Health check |

**This table is a curated subset, not the full route table.** A SAGA audit (2026-09-07) found
`main.go` registers ~128 distinct routes — this table covered roughly 20 of them, undercounting
several whole live subsystems. Real, additional, currently-served route families not itemized
above (see the actual `mux.Handle`/`HandleFunc` calls in `main.go` for the literal paths, and
`internal/http/handlers/` for the handler file — this list names the subsystem, not every path,
so it stays true without needing a line-by-line update every time a route is added):

- **GFD MMO backend** (`/api/v1/characters`, `/items`, `/guilds`, `/world-events`,
  `/fieldoffices`, `/hats`) — `internal/http/handlers/mmo*.go` (~2200 lines). Characters,
  inventory/equipment, guilds, world events, the WOTAN hat store (see
  `BRAWLPIT/docs/WOTAN_HAT_STORE_NORTHSTAR.md`).
- **Per-game ticketing/queues/leaderboards** for SHANKPIT, WEAKNIGHT_BEDROCK_RACERS, REDGARDEN,
  and PAPERCRAFT — real, live-match/ticket/queue/leaderboard routes, one handler file per game.
- **GFD admin content tools** — `gfd-items`, `gfd-mob-drops`, `gfd-registration`,
  `gfd-mob-spawns`, `gfd-dungeon-roster`: five real CRUD admin surfaces.
- **Organizations/cluster trust** (`/api/v1/organizations`) — `internal/http/handlers/
  organizations.go`, real (see `CarePyre/docs/HIPAA_COMPLIANCE_NORTHSTAR.md` for the design).
- **White-label branding** (`GET/PUT /api/v1/branding`) — `internal/http/handlers/branding.go`.
- **GDPR export/erasure** (`/api/v1/gdpr/`) — `internal/gdpr/`.
- **Compliance-recording storage** (`/api/v1/compliance/recording`) — `internal/http/handlers/
  compliance_recording.go`.
- **Notes, supply chain, research cache, kgraph, tenants, chat messages** — `notes.go`,
  `supply.go`, `research.go`, `kgraph.go`, `tenants.go`, `chat_messages.go`.

## Auth Model

- **Humans**: Google OAuth → `user_id` → roles → JWT with `roles[]` + `permissions[]`
- **Agents**: `agent_name` + `agent_secret` → JWT with explicit `permissions[]` (no role inheritance)
- Agents are registered in `config/agents.json` and seeded by `cmd/bootstrap`
- **Reveal a "training key"** (an agent's plaintext M2M secret) from the Back Office: `/admin/agents` → "Reveal secret" on any agent row with a credential set. The `agents` table only ever stores a one-way hash — this reads the plaintext back out of `var/agent-secrets.env` instead (`internal/agentsecrets`), the one place it's still recorded (founder real-time, 2026-09-25: "it needs to reveal them to me like an admin in carepyre can summon the email password out of the void"). Only works for an agent whose secret was set via `cmd/bootstrap` or rotated via this same admin UI since this feature landed — an older secret rotated some other way genuinely isn't recoverable; "Generate Secret" makes it revealable going forward. The reveal action itself (never the secret value) is audit-logged as `iduna:admin.agent.secret_reveal`.

## Directory Layout

```
cmd/
  bootstrap/    — seeds agents + initial users from config/
  bob-agent/    — MySQL schema admin agent (destructive ops require confirm: true)
  create-admin-agent/ — one-shot CLI, provisions a new agent with `iduna.admin` for signing into
                   `/admin/login` (Back Office). `go run ./cmd/create-admin-agent -name NAME`
                   prints the plaintext secret once — it's never retrievable again. Real, existing
                   tool for the recurring "no agent has iduna.admin, how do I get in" gap; a
                   credential-granting action, so a sandboxed agent needs the human to actually
                   run it, not just be told it exists.
internal/
  auth/         — JWT issuance, validation, Google OAuth flow
  http/handlers/ — route handlers (apples, heimdal, push-tokens, intelligence, admin, mmo*,
                   organizations, branding, gdpr, compliance_recording, notes, supply,
                   research, kgraph, tenants, chat_messages, and more — see the endpoint
                   list above for what's actually live; this directory has grown well past
                   what a short list here could stay accurate against)
  store/        — database layer (SQLite truestore + migrations)
  gdpr/         — GDPR export/erasure pipeline
  blog/, vault/, tenantprovision/, honorcode/, statuspage/, backlog/ — additional real
                   packages backing specific route families above; not exhaustively
                   itemized here for the same reason as http/handlers/ above
migrations/
  truestore/    — SQL migrations (timestamp-prefixed, append-only)
config/
  agents.json   — registered agents and their permissions
```

## Database

SQLite, default path controlled by `IDUNA_DB_PATH`. **Real, found-live correction (2026-09-07
SAGA audit)**: the live, actually-used database file on this box is `var/iduna.db`, not
`var/truestore.db` — `truestore.db` sits at a few KB, untouched since early August, while
`iduna.db` is the real, actively-growing (20+ MB) file every handler above actually reads/writes.
Whatever value `IDUNA_DB_PATH` is set to in this environment's own systemd unit/env file governs
which path is real; don't assume the bare word "truestore" in a path always means the live DB.
Migrations in `migrations/truestore/` are applied in filename order at startup against whichever
path is actually configured. **Never edit migration files after they've been applied — add new
ones.**

## Key Env Vars

```
IDUNA_DB_PATH        — default: ./var/truestore.db
IDUNA_JWT_PRIVATE_KEY — ES256 private key (PEM)
IDUNA_JWT_KEY_ID      — key ID embedded in JWTs
GOOGLE_CLIENT_ID
GOOGLE_CLIENT_SECRET
GOOGLE_REDIRECT_URI
GOOGLE_DRIVE_SERVICE_ACCOUNT_JSON — service account key JSON for Drive API (optional; Drive disabled if absent)
GOOGLE_DRIVE_FOLDER_ID            — Google Drive folder ID for training artifact uploads (optional; root if absent)
IDUNA_HEC_TOKEN       — bearer token for POST /services/collector (unified logging backend ingest); unset disables ingest entirely
EMILY_BACKLOG_PATH    — path to EMILY/BACKLOG.md for the kanban board's two-way sync (inbox read + new-card write-back); default /home/fatbaby/EMILY/BACKLOG.md
NOCK_VIDEO_DIR        — on-disk root for NOCK video clips/proxies/renders; default /home/fatbaby/IDUNA/var/nock-videos
NOCK_VIDEO_MAX_MB     — per-upload size cap for NOCK video uploads (phone + desktop); default 4096
SSO_GOOGLE_REDIRECT_URIS — comma-separated list of full callback URLs, one per host the SSO login page can be reached at; each must be registered in Google Cloud Console; default "<BASE_URL>/api/v1/auth/sso/google/callback,https://iam.okemily.com/api/v1/auth/sso/google/callback"
BNET_CLIENT_ID / BNET_CLIENT_SECRET / BNET_REDIRECT_URI — Battle.net OAuth app (human-only registration); unset = provider disabled (404); redirect default BASE_URL + /api/v1/auth/sso/battlenet/callback
TWITCH_CLIENT_ID / TWITCH_CLIENT_SECRET / TWITCH_REDIRECT_URI — Twitch app (login + link, no scopes either way); redirect default BASE_URL + /api/v1/auth/sso/twitch/callback
SSO_ALLOWED_REDIRECT_HOSTS — comma-separated hostnames /api/v1/auth/sso/login's redirect_uri is allowed to target; default "wotan.okemily.com,localhost,127.0.0.1"
```

## Apples

Apples are the golden documentation audit trail. Filed by emily-agent after each RSI cycle.
Also backed up to `github.com/emilyspringerton/APPLES` via `emily sync --apples-git-dir`.

Apple types: `improvement`, `observation`, `audit`, `escalation`, `completion`, `backlog_completion`.

## HEIMDAL Sprints

Sprint lifecycle: `pending` → `queued` → `in_progress` → `complete` | `blocked`.  
MJOLNIR submits requirements (pending) → Emily Prime translates to RSI item (queued) →
Claude Code executes → Emily Prime patches on completion (complete/blocked).

## Unified Logging Backend

"One real place to jump to and grab the logs" (founder real-time, 2026-09-02) — a real,
Splunk-shaped event log, separate from the user-event log above (that one is scoped to IDUNA
local users specifically). Backed by `internal/userlog.FileEventLog` (the same real NDJSON
append-only log the user-event log itself uses, just a separate root dir), stored under
`var/eventlog/` — real, checked-not-assumed reason this reuses `userlog` rather than
`PRRJECT_FATBABY`'s own `eventstore` package (the more "original" implementation of the identical
shape): IDUNA's real CI checks out this repo standalone with no `go.work`/sibling-repo present,
confirmed via `GOWORK=off go build ./...`. See `internal/http/handlers/logs.go` for the real
scope: ingest (`POST /services/collector`, Splunk HEC's own real endpoint path/auth/payload/
response shape) and search (`GET /services/search/jobs`, Splunk's own real endpoint path,
deliberately synchronous — a real, narrow SPL subset: `type=`/`source=`/`q=` terms plus a real,
separate `regex=` parameter, RE2-based so it's not a ReDoS vector). A real log query UI lives in
the developer portal at `/portal/logs` (`internal/http/handlers/portal.go`'s own `Logs` method) —
same search/regex query, rendered as a real HTML form + results table; requires IDUNA itself to
be up (a real, deliberately accepted limitation while migration is planned, named on the page
itself, not hidden).

Real code paths that emit events today (S226-01 through S226-04, `EMILY/BACKLOG.md` SECTION 226 —
closed): `GoogleAuthHandler`/`AgentAuthHandler`/`LocalAuthHandler`/`AdminLoginHandler`/
`PortalHandler.LocalLogin` (every real login surface — `iduna:auth.*.success`/`.failure`);
`AdminHandler.userAction`/`agentAction` suspend/activate (`iduna:admin.user.suspend`/`.unsuspend`,
`iduna:admin.agent.suspend`/`.unsuspend`) and the same handler's role assign/revoke
(`iduna:admin.role.assign`/`.revoke`), agent permission grant/revoke
(`iduna:admin.agent_permission.grant`/`.revoke`), and agent secret rotation
(`iduna:admin.agent.secret_rotate` — records that a rotation happened, never the freshly-generated
plaintext); `HeimdalHandler.submit`/`.patch` (`iduna:heimdal.submit`, `iduna:heimdal.transition`
with real `from_status`/`to_status`); `ApplesHandler.create` (`iduna:apples.create`);
`KanbanHandler.create`/`.update`/`.completeCard` (`iduna:kanban.card.create`/`.move`/`.complete`
— kanban card 3243242, "ensure kanban does log streaming and checks in to the unified log").
Every
emission point is nil-safe (an unset `EventLog` field is a no-op, not a panic) and fire-and-forget
(a logging-backend outage never breaks the real auth/admin/heimdal/apples flow it's observing).

## Emily for Business — product-scoping (not implementation)

`docs/EMILY_FOR_BUSINESS_NORTHSTAR.md` (S243-02, 2026-09-03) — real, grounded scoping input for
the founder's own framing "IDUNA IS THE PRODUCT BASICALLY ZERO TRUST SECURITY AGENT NATIVE."
Names the real tension against this file's own standing "IDUNA is not a product, it is the
backbone" framing (`docs/NORTHSTAR.md`), maps IDUNA's real existing primitives onto a zero-trust
pitch, and names what's genuinely missing (multi-tenancy, self-serve onboarding, continuous
posture verification, compliance attestation) plus open questions for a founder-level decision.
Not an implementation plan — no code changes follow from this doc alone.

## Model Repositories — git-lfs Integration

Founder real-time, 2026-09-22: "we need to integrate the model repository with git lfs and each
model repository should have a git integration that can be turned off (on by default)."

Every RL checkpoint registry ("model repository" — `internal/brawlpit.CheckpointStore`, shared by
BRAWLPIT and DEADWEIGHT via its own `Game` field, and `internal/shankpit.CheckpointStore`) now
syncs each newly-created checkpoint blob into a real sibling repo checkout on this box
(`/home/fatbaby/<BRAWLPIT|DEADWEIGHT|SHANKPIT>`), under `models/rl-checkpoints/`, git-lfs-tracked
— `internal/modelgit.Syncer`. Fire-and-forget (a git/network failure never fails or blocks the
real checkpoint upload), reusing `internal/gitsync.PushWithRetry` (extracted from apples.go's own
production-proven Apples-git-sync idiom) for the actual add→commit→push→retry-on-rebase.

**On by default, per-game toggle**: `<GAME>_MODEL_GIT_DISABLED` (any non-empty value) turns the
integration off for that one game; unset (the default) is enabled — a `modelgit.Syncer{}` zero
value is enabled by construction, matching the founder's own "on by default" requirement without
needing a constructor at every call site. `<GAME>_MODEL_GIT_REPO_DIR` overrides the destination
repo path per game if the default sibling-checkout path is ever wrong for a given deployment.

## Migrations Checklist

- Migration filenames: `YYYYMMDDNNNN_description.sql`
- Never modify an applied migration
- `cmd/bootstrap` runs all pending migrations at startup

## Related Repos

- `EMILY` — Emily Prime agent (primary Apple filer, HEIMDAL processor)
- `PRRJECT_FATBABY` — signal pipeline (downstream JWT consumer)
- `MJOLNIR` — Android app (HEIMDAL submitter, push token registrar)

## Apple Filing Protocol

After any meaningful change, file an Apple:
```bash
emily apples post -t completion "<title>" "<body with commit hash>"
```
Then mark the item done in EMILY/BACKLOG.md and commit: `git add BACKLOG.md && git commit && git push`

## CHANGELOG Protocol

After any meaningful change, update CHANGELOG.md:
```bash
emily changelog add IDUNA "<what changed>"
# or manually: append a dated bullet under ## YYYY-MM-DD in IDUNA/CHANGELOG.md
```

## Golden Doc Registration

If you create a new NORTHSTAR.md, architecture spec, or mission-critical design doc in this repo,
append a row to `EMILY/context/golden-docs-index.md` so Emily Prime picks it up on the next cycle:
```
| NAME | <repo>/path/to/doc.md | 1 | <budget-or-0> | one-line description |
```
Then commit and push EMILY:
```bash
cd /home/fatbaby/EMILY && git add context/golden-docs-index.md && git commit -m "golden-index: add NAME" && git push
```

## SHANKPIT Level Registry Doubles as Living Documentation (standing instruction)

Founder real-time, 2026-09-17: "write into your claude files that when you verify levels write
them into the registry and leave them there as an example designers can use to try to figure out
how to use the feature without blowing a bunch of tokens asking for help." This registry
(`shankpit_levels`/`shankpit_widgets` tables, served under `/admin/nock/api/shankpit-levels` etc.,
gated by `iduna.admin`) is currently being treated as a development/staging registry — "assume
this is the development level registry we are developing in the open." Whenever a
SHANKPIT-level-editor feature is verified here, the verification should land as a real,
clearly-named level/widget row in this same live registry (e.g. `TUTORIAL_DOOR`) rather than a
disposable local JSON export, so it stays discoverable to designers via the NOCK UI afterward. See
`SHANKPIT/CLAUDE.md`'s own copy of this instruction for the full rationale. A separate registry
for real user-created content, and cloning this one's contents (including story levels) forward
into it, is a real possibility the founder named for later — not something to build preemptively.

## Founder Real-Time Direction

Whenever the founder gives real-time direction — a new ask, a correction, a "can we also..." —
route it through `emily observe -s info "Founder real-time: <summary>"` first, even if it isn't
this repo's usual domain, then sprint-plan it into `EMILY/BACKLOG.md` (`emily backlog curate`,
scoped into a real SECTION/sub-item, not just a one-line log), and only then implement. See
`EMILY/docs/THE_EMILY_WAY.md` Principle 18 ("Pave the Cow Paths").

## README Reality — SAGA reconciliation (standing instruction, monorepo-wide)

Founder real-time, 2026-09-18: if a change of yours **substantially changes the claim of this project's core README**,
then per SAGA protocols (`EMILY/docs/SAGA_SYSTEM_AUDIT_2026-07-18.md`, HQ-SPEC-DOC-102: intent ↔ claim ledger ↔ reality)
you **must update `README.md` in the same unit of work** so it reflects current reality. The README is the project's public
claim; it must not lag behind the code.

- **When it applies:** a capability is added or removed; status moves ("design only" → "working", "planned" → "shipped");
  the stack, build, run or install steps change; a claim in the README is now false or stale; or you add a **meaningful,
  genuinely interesting piece of kit** (a new tool, engine capability, protocol, pipeline, game system). For that last case
  especially: put it in the README — what it is, how to run it, and its honest status and limits.
- **When it does not:** ordinary fixes, refactors and small features that leave the README's claims true.
- **How:** re-read the README against what you just changed; fix or delete stale lines (including "not built yet" notes that
  are now built); verify any new claim by actually running it, and mark anything untested as untested; commit the README
  with (or immediately after) the change, and mention it in the CHANGELOG entry.

## Frame-Break Reframing

Founder-sourced prompting technique (REDGARDEN/NORTHSTAR.md §28, full origin in
REDGARDEN/docs2/MULTI_AGENT_RD_RESEARCH_NOTES.md §5): given a request, name the underlying
structural/systemic pattern it's one instance of — one level of abstraction up — as an added
lens during planning/triage/judgment calls. Use it to spot the general case behind a specific
ask. It augments judgment, it does not replace doing the work: direct, concrete execution of
the literal task asked for still happens every time.

## CONSTRUCT File Generation (standing instruction, monorepo Principle 21)

**IDUNA auto-generates deterministic CONSTRUCT and MANIFEST files on every push** via `scripts/generate_iduna_construct.sh` (integrated into CI). The CONSTRUCT is a plaintext snapshot of all tracked source files with SHA256 hashes, sizes, and git modes — used for reproducible builds, audit trails, and offline access.

Key features of IDUNA's implementation:
- Uses `git ls-files` for determinism (same git tree always produces identical output)
- Includes a MANIFEST file with per-file metadata (sha256, size, git mode)
- Base64-encodes binary files automatically
- Excludes generated artifacts (CONSTRUCT files themselves, build/, dist/, vendor/, etc.)

No manual work needed — generation is automatic. See the main `CLAUDE.md`'s "Principle 21: CONSTRUCT Files" section for the full monorepo pattern and how it integrates with releases.

## Commit Protocol (standing instruction)

Always commit and push completed work immediately — don't wait to be asked. This is the default for every repo in this monorepo.

Every commit — human-written or produced by automated code paths (git-commit helpers in emily-agent, emily.cli, IDUNA handlers, etc.) — must carry the active `emily session` fingerprint as a `session: <tag>` trailer (blank line, then the trailer). This was silently missing from several independently-implemented automated commit helpers across the monorepo until an audit on 2026-08-10 (founder, real-time: "where in the fuck is my llm session id anywhere"). If you add a new automated git-commit code path anywhere, wire in the session tag the same way — don't assume an existing helper already does it.

## Core Deps Are PARENA-First (standing, monorepo-wide)

Founder real-time, 2026-10-01: *"always implement core deps in PARENA — when core deps are missing
always implement the core deps in PARENA first."*

- **When a core dependency is missing** (a codec, a protocol client, an inference engine, a
  parser, a data structure — anything this repo's own functionality stands on), implement it in
  PARENA (`PARENA/stdlib/...`) **first**, before building the feature that needs it. Deps first,
  feature second.
- **If PARENA itself can't express the dep yet**, that gap is the real first task: fix or extend
  PARENA (compiler, emitter, or stdlib), with tests, then build the dep on top. Don't route around it.
- **Third-party tools/binaries are stopgaps, not the answer.** Shelling out to or FFI-binding an
  existing tool is allowed only to unblock a demo, and must be labeled as a stopgap in the code and
  in `EMILY/BACKLOG.md` with a PARENA replacement item. (Example: Piper via subprocess for
  MODE_TYLER TTS, 2026-10-01 — stopgap; the PARENA-native synthesis stack is the real work.)
- **Not a license to reimplement the OS.** Core deps = what the product's own behavior depends on.
  Compilers, kernels, system libraries and the like stay as-is; a repo's own CLAUDE.md may record a
  considered, specific exception (same standard as the LZ4 compression convention).
