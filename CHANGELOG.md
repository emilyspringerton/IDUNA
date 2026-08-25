# IDUNA Changelog

## 2026-08-25
- Restyled /portal login + home to the real IDUNA cream/gold style guide with Prompt-o-verse art (fenrir-robot, fox-robot); added /portal/logout (sess-20260825-0828-cc32a704)
- New developer notebook portal: GET /portal/login (Google SSO) + GET /portal (gated by new devportal.access permission, granted to nobody by default) -- lists Jupyter and SARENA_NOTEBOOK, both not-yet-available (Apple #15924) (sess-20260825-0828-cc32a704)
- Fix: RequireCookieAuth no longer treats human Google-login cookie sessions as agent sessions during the live-status recheck (was bouncing every human session to login) (sess-20260825-0828-cc32a704)
- GoogleAuthHandler sets HttpOnly iduna_session cookie on login; RefreshHandler accepts/refreshes it -- auth foundation for the planned Jupyter/SARENA notebook portal (Apple #15918) (sess-20260825-0828-cc32a704)
- The ladybug admin-session-revocation regression spec now actually compiles, links, and runs (passed=1 failed=0) against a real fixture WebDriver server. Apple #15914. (sess-20260825-0828-cc32a704)
- SECURITY FIX: suspended agent's live Back Office session is now actually revoked -- RequireCookieAuth re-checks live agent status+permissions every request, not just at login. Apple #15908. (sess-20260825-0828-cc32a704)
- Drive slurp feature (S187-03/S188-05/S189-10): OAuth-based Back Office Drive browse+slurp, background job queue with idempotency, SSE progress (sess-20260825-0828-cc32a704)
- Migrated EDDY admin credential (Back Office iduna.admin secret) from plaintext ~/.ssh/iduna-admin-eddy.txt into IDUNA Vault as api_key item #1; plaintext file deleted. (sess-20260825-0828-cc32a704)

- S191-02: admin session sliding-expiration refresh — fixes silent hard-cutoff logout on the 8h admin cookie (founder-reported 'logged out when i shouldnt be'), reissues on activity once past half the TTL. Apple #15775. (sess-20260825-0828-cc32a704)


## 2026-08-20

- Sign every filed Apple with anchor + snowman (⚓ ☃), enforced server-side at POST /api/v1/apples (signAppleBody) per founder standing order (sess-20260820-0649-a3f19d93)


## 2026-08-18
- Added Store.MergeTags + PATCH /api/v1/promptoverse/nodes/{slug}/tags for backfilling node metadata (sess-20260813-2154-dda37e8b)
- Added /admin/promptoverse-queue Back Office page for fixing/queuing Prompt-o-verse generation queue entries without CLI access (sess-20260813-2154-dda37e8b)
- Auth strip now proactively refreshes its token via IDUNA's existing /api/v1/auth/refresh, plus retries once on a real 401 (sess-20260813-2154-dda37e8b)
- New cards/sections stagger in with a jittered fade-in instead of snapping in synchronously (sess-20260813-2154-dda37e8b)
- Auth strip now shows a visible 'Sign in (coming soon)' placeholder instead of rendering empty when GOOGLE_CLIENT_ID isn't set (sess-20260813-2154-dda37e8b)
- Fixed the real live-reload bug: subject/style pages never had a poll script at all (only the index page did). Added node_variants (regenerate-with-variation, additive, same page) (sess-20260813-2154-dda37e8b)
- Site-wide auth strip (sign-in funnel) on every Prompt-o-verse page — header pill shared across node/index/subject/style pages via one localStorage token (sess-20260813-2154-dda37e8b)
- Mashup nomination social tool: authenticated (Google ID-token) users nominate combining two subjects, admin-only approve/reject via new promptoverse.mashups.review permission, autocomplete widget on every subject page (sess-20260813-2154-dda37e8b)
- Fixed stale live-reload JS: iduna.service was running a binary built 21s before the incremental-DOM-patch fix landed, restarted with current code (sess-20260813-2154-dda37e8b)
- Renderer shows Mashups cross-links on subject/style pages, reading the new LLM-judgment cache from emily.cli (sess-20260813-2154-dda37e8b)
- fix(promptoverse): gallery index no longer re-renders on every live-update poll tick when the node list hasn't actually changed -- was causing visible flicker, especially the first card in each category (sess-20260813-2154-dda37e8b)
- feat(promptoverse): new cmd/promptoverse-thumbnails (systemd timer, 15min) idempotently generates a thumbnail + JPEG-optimized version of every node's image via ImageMagick convert, re-rendering afterward. Renderer resolves GalleryImageFile/HeroImageFile at render time (thumb/optimized if present, original otherwise); live-update JS falls back via onerror. Live: 1.9MB PNGs became 236KB optimized JPEGs + 24KB thumbnails. (sess-20260813-2154-dda37e8b)

- feat(promptoverse): GET /api/v1/promptoverse/discovery -- public read-only endpoint combining the hardcoded style registry (exported from emily.cli), discovered styles, GPT-2 candidate tags, and content-policy dead-letter entries (sess-20260813-2154-dda37e8b)


## 2026-08-17 (3)
- feat(promptoverse): gallery index now live-updates every 10s (fetch/setInterval, same idiom as OKEMILY/tournaments.html's hero leaderboard) via the existing GET /api/v1/promptoverse/nodes endpoint. New Style pages at /prompt-o-verse/style/<label-slug>/ (mirrors Subject pages but for the Label axis, always generated -- no leaf-count threshold), linked from every leaf's h1 and from index category headings. (sess-20260813-2154-dda37e8b)
- fix(promptoverse): subject page <img src> used a bare relative path that only resolves correctly from the top-level index, not from subject/<slug>/ which is one directory deeper -- every subject page's images 404ed. Switched to the absolute /prompt-o-verse/<slug>/<file> path already used by the <a href>s in the same template. Re-ran cmd/promptoverse-rerender to fix already-published pages. (sess-20260813-2154-dda37e8b)
- feat(promptoverse): subject-grouping (leaf pages with a Subject that has ≥ 2 published leaves get a linked '/subject/<slug>/' page and a clickable Applied-to line); RenderAll now re-renders every node + subject pages on every publish (not just the new node + index) so an OLDER sibling gains its link the moment a SECOND leaf under the same Subject goes live; added cmd/promptoverse-rerender (mirrors cmd/blog-rerender) and used it to backfill all 28 existing nodes, which also fixed a stale published_at=0001-01-01 baked into early VS0 leaf pages from before RenderAll existed (DB values were always correct -- the static HTML just never got re-rendered with them until now) (sess-20260813-2154-dda37e8b)

- feat(prompt-o-verse): add a real taxonomy level -- `Label` is now the style/subcategory (e.g.
  "Renaissance oil painting"), `Subject` is what it's applied to (e.g. "baseball card", "Master
  Chief (Halo)"), and each node carries both an `EZPrompt` (short, e.g. "renaissance oil painting
  master chief halo" -- what a normal/vanilla pipeline would receive unenriched) and the real
  `ExpandedPrompt` it was actually generated from, formalizing the northstar's own two-tier
  prompting model (§3) as real fields instead of one undifferentiated prompt string. Index page
  rewritten to group leaf nodes by `Label` (`<section>` per style, semantic heading + count),
  proving styles generalize across subjects instead of looking baseball-card-specific -- every
  leaf keeps its own dedicated page regardless of grouping (SEO, per founder direction). Seeded a
  new Master Chief (Halo) batch reusing 9 existing styles (1910s tobacco card, stained glass,
  8-bit pixel art, Renaissance oil painting, LEGO minifigure, claymation, pop art silkscreen,
  woodcut, watercolor) via Vertex AI -- 3/9 succeeded before hitting sustained rate limits, stopped
  there per founder's own "proceed with what you have" call rather than continuing to hammer the
  limiter. Wiped and reseeded all 20 live nodes (17 baseball card + 3 Master Chief) against the
  new schema. 2 new regression tests (shared-Label grouping, singular/plural variant count) +
  updated existing tests for the renamed/added fields. `go build`/`vet`/`test ./...` clean.
  Live-verified: shared styles now show "2 variants" on the index, e.g. the tobacco-card style
  applied to Master Chief renders a genuinely convincing "MASTER CHIEF · SPARTAN II · UNSC"
  Victorian-bordered lithograph card.

## 2026-08-17 (2)

- feat(prompt-o-verse VS0): new `internal/promptoverse` package + `PromptOVerseHandler`, same
  own-SQLite/render-to-static shape as `internal/tyler`, wired into okemily.com at
  `/prompt-o-verse/`. Each node carries exactly 3 pieces of data per the founder's own contract —
  top-level prompt, generated image, labeled taxonomy tags — rendered with real semantic HTML
  (`<article>`, `<figure>`/`<figcaption>`, `<dl>`/`<dt>`/`<dd>` for tags, `<time datetime>`), not
  div soup. New `promptoverse.write` permission (migration `202608170002`), granted to EMILY-PRIME.
  Seeded live with a real 20-top-level-prompt VS0 MVP batch (baseball card photography, 3 real
  historical eras + 17 fun/surreal transformations aimed at people new to generative AI, per
  founder direction) generated via Vertex AI's `gemini-2.5-flash-image` — 17/20 succeeded before
  hitting real rate limits (429s), 3 queued to backfill later per founder's own "proceed with what
  you have" call. `okemily-deploy.sh` updated to exclude `prompt-o-verse/` from its rsync (same
  blog/tyler live-render protection, would otherwise get wiped on next deploy). 11 new tests
  (store CRUD/dedup/ordering, renderer semantic-HTML/index output). `go build`/`vet`/`test ./...`
  clean. Live-verified on okemily.com, real generated images visible.

## 2026-08-17

- fix(S157-01): grant EMILY-PRIME the `heimdal.process` permission it was always meant to have.
  `PATCH /api/v1/heimdal/sprints/{id}`'s own doc comment says "(heimdal.process — Emily Prime)"
  and the permission itself has been seeded in the catalog since `202606090003_heimdal_sprints.sql`
  ("Process HEIMDAL sprints and update status (Emily Prime only)"), but `config/agents.json` never
  actually granted it — no agent could ever call that endpoint. This is why sprints 1/2/3 sat
  `pending` for two months: not blocked on HITL-11's dead credit balance as assumed, but on a
  permission that was designed-for and documented but never wired. Added `heimdal.process` to
  EMILY-PRIME's permission list, re-ran `cmd/bootstrap` (dry-run first to confirm no credential
  rotation/destruction — the S141-04 `writeSecretsEnv` merge fix holds), verified live: a freshly
  minted EMILY-PRIME JWT carries the permission, `PATCH` on all three stale sprints now succeeds.
  Reconciled all three by hand against real, already-confirmed BACKLOG.md state (S21-03, S24-01,
  S23-01) rather than waiting on HITL-11's haiku auto-translate — see EMILY/BACKLOG.md S157-01.
  (sess-20260813-2154-dda37e8b)

## 2026-08-16

- feat(S153-11 partial): `GET /api/v1/status/history?target=<name>&hours=<n>` — raw check history
  (up/down + latency_ms, chronological) for one status target, capped at 500 samples / 168 hours.
  `statuspage.Store.History` backs it; no schema change, every check has always been retained
  (see `Store.UptimePercent`'s own doc comment) — this just exposes the rows directly instead of
  only a rolled-up percentage. `status.html` renders it as a per-service incident-timeline strip
  (colored bar per check, hover for exact time/status/latency) — the two candidates S153-11 named
  as already-buildable off the existing data model. Live-verified against real production history
  through `okemily.com`'s public proxy (61 real checks over 1h for `iduna`). Company-cap/latency-
  graph-as-a-chart and a public postmortem/incident log remain open, not attempted here. 9 new
  tests (`Store.History` + `StatusHistoryHandler`), `go build`/`vet`/`test -race ./...` clean.
  (sess-20260813-2154-dda37e8b)

## 2026-08-14

- New Renderer.RenderManifest: live blog manifest text file at okemily.com/blog-manifest.txt, wired into publish path + cmd/blog-rerender (sess-20260813-2154-dda37e8b)


## 2026-08-10

- 新增 CarePyre 聯絡表單後端：POST /api/v1/carepyre/contact(公開、CORS+rate-limit)寫入 carepyre_contact_submissions,並在 Back Office 新增 /admin/carepyre 檢視頁(暫不接 email 通知,依創辦人指示先縮小範圍) (sess-20260809-1420-e9d3d7f8)


## 2026-08-06
- Extended /api/v1/chat/messages (originally mud<->battlegrounds) to support the GFD<->EINHORN_SURVIVAL chat bridge (S171-04) -- new gfd_server/einhorn_survival sources, gta7 channel. No new endpoint/permission/agent needed. (sess-20260723-2347-df115bd5)

- New TYLER reading room (internal/tyler) -- dedicated okemily.com/tyler/ pages for TYLER episode scripts, real markdown rendering (headers/bold/tables/checklists), IDUNA-style-guide theme, speechSynthesis audio button. tyler.write permission granted to EMILY-PRIME. All 5 existing Series X interludes published. (sess-20260723-2347-df115bd5)


## 2026-08-05
- Registered GTA7-SERVER agent (apples.write) for the GTA7 Paper plugin -- direct HTTP Apple posting + WOTAN-shared player_id registration, replacing GTA7's earlier CLI-shell-out shortcut. (sess-20260723-2347-df115bd5)
- Back Office expansion: fixed /admin/ 404 (nginx trailing-slash gap), dashboard (quick actions + mailing-list signup stats), /admin/dragonsnshit/create, first Game Master tool (/admin/gm disable/enable, players.disabled_at enforced at login) (sess-20260723-2347-df115bd5)
- cmd/create-admin-agent -- provision a human-operator Back Office login (agent_name + agent_secret with iduna.admin). Created EDDY as the founder's own admin login. (sess-20260723-2347-df115bd5)
- /admin/saga -- SAGA divergence queue page (S143-03 first slice): vaporware debt + dark matter per repo, via emily saga gaps --json (sess-20260723-2347-df115bd5)

- PATCH /api/v1/characters/:id/job -- persist real job_main/job_sub (closes a gap where setjob never wrote back to IDUNA) (sess-20260723-2347-df115bd5)


## 2026-08-04 (3)
- feat(mmo): persist and return a character's real Home Point. New PATCH /api/v1/characters/:id/home
  (mirrors /position); characterResponse + GET handlers now include home_scene_id/home_pos_x/y/z.
  Fixed a real test fixture gap (mmo_inventory_test.go schema) this surfaced.

## 2026-08-04 (2)
- feat(auth): create a real DragonsNShit character atomically on email register. Founder: "i need
  a way to create dragonsnshit accounts for testing - i need iduna login i think it should live
  in iduna create account for dragonsnshit." New optional `character_name`/`character_job` fields
  on `POST /api/v1/auth/email/register` -- when set, the same request also inserts a real
  `characters` row (same shape `mmo.go`'s own `handleCreateCharacter` uses) in the same
  transaction, returning `character_id`/`character_name` alongside the real login credentials.
  Replaces this session's own repeated raw-SQLite-INSERT test-character habit with a real,
  reusable feature. Live-verified end-to-end: register -> login -> `GET /api/v1/characters/:id`
  -> a real character usable immediately against apps2/mud's own `/api/town/command`. Also found
  and fixed a real operational issue while deploying: an orphaned, untracked `iduna` process from
  2026-08-03 was squatting on `:8080`, causing every systemd-managed restart to silently
  crash-loop on "address already in use" while the stale binary kept serving all traffic --
  killed it; `iduna.service` runs under real supervision again.

## 2026-08-03 (4)
- ops: bootstrapped webmaster (uid=0) for the first time on this box -- founder: "make me a new
  account eli@okemily.com pw testtest." `local_users` was completely empty (no webmaster.json had
  ever existed at `var/webmaster.json`), so `POST /api/v1/users` (requires `users.admin`, only
  uid=0 has it) had no real credential able to call it at all. Seeded `var/webmaster.json`
  (gitignored, real random 24-char password, not committed) with `webmaster@okemily.com`,
  restarted `iduna` (manual kill+relaunch sourcing `~/.config/iduna/env`, same env the systemd
  user unit uses -- `systemctl --user` itself isn't reachable in this shell) so
  `userlog.SeedWebmaster` ran and created uid=0. Then created the real requested account
  (`eli@okemily.com` / local_uid=1) via the real `/api/v1/users` API, authenticated as webmaster
  -- not a direct DB write, so the event log/projector stay consistent with everything else that
  reads local_users. Confirmed both via `/health` and a real `/api/v1/auth/local` login as `eli`.

## 2026-08-02 (3)
- fix(mmo): add real `PATCH /api/v1/characters/:id/level` route. Found live building
  GoblinFoxDragon's Town headless-combat feature: `idunaclient.UpdateCharacterLevel` has always
  called plain `PATCH /api/v1/characters/:id` -- a route that has never existed on this side
  (only `/position`, `/gold`, `/gold/credit`, `/skills` do), silently 404ing and masked by
  "best-effort" error handling at every call site. This means every real telnet character's
  level/XP has never actually persisted across `apps2/mud` process restarts, this entire time --
  a level-up only ever lived in that connection's own in-memory `player` until now. Added the
  real route + `handleUpdateLevel`. Agent-only (unlike `handleUpdatePosition`'s player-self-update
  allowance): self-reporting your own level/XP is a cheat vector no client should be trusted
  with, so even the *owning* player's own JWT is rejected here, not just a non-owner's. 5 new
  tests, full suite green.

## 2026-08-02 (2)
- fix(mmo): `PATCH /api/v1/characters/:id/position` now checks ownership for player-JWT callers.
  GoblinFoxDragon "unify the whole bitch" (Town scene syncing position back to the real
  character record) means this endpoint -- doc-commented "game server M2M" and, until now, only
  ever reached by apps2/mud's trusted agent JWT -- is about to be called directly by a compiled
  client running on a player's own machine for the first time. `RequireAuth` alone (any valid
  JWT, no ownership check) was fine while only a trusted backend reached it; a real player JWT
  moving an arbitrary character_id it doesn't own wasn't previously possible to prevent because
  nothing checked. Agent JWTs (identified by the `agent_name` claim only `POST /api/v1/auth/agent`
  issues, same distinguishing field `AgentAuthHandler` already sets) are unaffected -- apps2/mud's
  own position-sync call keeps working exactly as before. A caller with no claims in context at
  all (direct-to-handler calls bypassing `RequireAuth`, same shape this package's own existing
  gold-endpoint tests already use) is also unaffected -- nothing to check without an authenticated
  context. 4 new tests, full suite green. Live-rebuilt and restarted; confirmed the in-progress
  REDGARDEN match (`red_garden_arena_server` --port 7303) was untouched by the restart.

## 2026-07-31 (2)
- feat(redgarden): `POST /api/v1/redgarden/self-ticket` -- closes `REDGARDEN_GUI_NORTHSTAR.md`'s
  own named gap ("No GUI login path exists yet end-to-end"). Same "mint for the caller's own JWT
  subject" trust model as `ShankpitTicketHandler`, deliberately separate from
  `RedgardenPlayerTicketHandler` (mints on behalf of a request-body player_id, restricted to the
  DRAGONSNSHIT-MUD agent) so that handler's own blast-radius guarantee stays untouched. REDGARDEN
  `apps/arena`'s new login screen calls this directly after `/api/v1/auth/email/login`. 404 if
  the authenticated player has no registered DragonsNShit character yet. 6 new tests. `main.go`,
  `internal/http/handlers/redgarden_self_ticket.go`.

## 2026-07-31
- feat(mmo): `GET /api/v1/characters/by-player/:player_id` -- REDGARDEN_GUI_NORTHSTAR.md
  Milestone 4 (reward-credit hook). Resolves a WOTAN player_id to its DragonsNShit character, if
  it has one -- REDGARDEN's `apps/arena_server` (`report_match_result`) only knew match
  participants' player_ids from their connect tickets, with no way to find the character_id its
  gold-credit call needs. Same shape as the existing `GET /api/v1/characters/:id`, keyed by
  `player_id` instead; checked ahead of that route's own prefix match so it doesn't get treated
  as a literal character_id. No new permission -- same generic `RequireAuth` every characters
  route already uses. 3 new tests.
- feat(redgarden): `POST /api/v1/redgarden/player-ticket` -- REDGARDEN_GUI_NORTHSTAR.md
  Milestone 3 (Battlegrounds entry point). Mints a real REDGARDEN connect ticket for a real
  DragonsNShit character's own `player_id`, the non-bot counterpart to the existing
  `redgarden.ticket.mint`/`RedgardenTicketHandler` (which is deliberately scoped to
  `redgarden_bot`-provider players only and stays untouched). New
  `redgarden.player-ticket.mint` permission, checked the opposite way: the player_id must have
  a real `characters` row instead of a `redgarden_bot`-provider `players` row, so neither
  permission can satisfy the other's trust model even if one agent's secret leaked. New
  `DRAGONSNSHIT-MUD` M2M agent (`config/agents.json`, migration
  `202607310001_dragonsnshit_mud_agent.sql`), provisioned live via `cmd/bootstrap` against the
  running SQLite truestore (idempotent, existing agents untouched -- verified live: new agent
  logs in via `/api/v1/auth/agent` against the running server with no restart needed). 5 new
  tests (`internal/http/handlers/redgarden_player_ticket_test.go`). Real, related, honest gap
  found while wiring this: GoblinFoxDragon's own `apps2/mud` was calling `CreateCharacter` with
  `conn.RemoteAddr().String()` (a TCP socket address) as `player_id` -- not a valid UUID, not
  stable across reconnects, and this ticket endpoint's own `uuid.Parse` would reject it outright.
  Fixed on the GoblinFoxDragon side (see that repo's own CHANGELOG), not here.
- feat(mmo): `PATCH /api/v1/characters/:id/gold/credit` -- the symmetric counterpart
  `handleDeductGold` never had. GoblinFoxDragon's own `docs2/DRAGONSNSHIT_TWO_BACKENDS_AUDIT.md`
  (EMILY/BACKLOG.md "unify the backends" item) traced a real gap to here: neither `apps2/mud`'s
  disconnect-time gold sync nor any future `apps2/server-go` reward-crediting (REDGARDEN
  Battlegrounds, per `GoblinFoxDragon/docs2/REDGARDEN_GUI_NORTHSTAR.md`'s own Milestone 4) can
  ever persist a gold *increase*, because this API had no way to grant gold at all -- only
  deduct. New `handleCreditGold`, same atomic-update shape as `handleDeductGold`, bounded by a
  new `maxGoldCreditPerCall` (10,000 -- a soft sanity cap; unlike deduction, which is naturally
  bounded by a character's own existing balance, a credit endpoint has no natural ceiling, so an
  unbounded one risks a single malformed/malicious call minting currency). 5 new tests
  (`internal/http/handlers/mmo_gold_test.go`): success case, rejects non-positive, rejects
  over-cap (balance verified unchanged), unknown character 404, and a regression guard
  confirming the new route doesn't shadow the existing deduct route. `go build ./...`/`go test
  ./...` clean.

## 2026-07-30
- feat(redgarden): live-match spectator endpoint. Founder: "i want to watch the match on my
  phone web view" -> "live text dashboard." A fourth REDGARDEN aggregate alongside game-result/
  hero-result/leaderboard, but deliberately NOT database-backed: this is ephemeral "what's
  happening right now" state (phase, resource race, node ownership, tower HP, per-hero HP/K/D/
  Flow), not a durable stat, so an in-memory mutex-protected holder is the honestly correct shape
  rather than churning SQLite every few seconds for data nobody needs to keep. New `POST
  /api/v1/redgarden/live-match` (requires `redgarden.match.write`, same permission every other
  REDGARDEN write handler uses -- the authoritative game server reporting its own state, a third
  aggregate over the same fact) stores only the latest snapshot; public `GET
  /api/v1/redgarden/live-match/latest` serves it back, reporting `{"live":false}` if nothing's
  been posted in the last 30s (a stale snapshot from an ended/crashed match reads identically to
  a real live one otherwise). Only one match runs at a time under the current bot-pool
  architecture (a single 20-slot lobby), so "the latest reported match" is unambiguous. Verified
  live end to end: `apps/arena_server` now posts every 3s while `ARENA_PHASE_LIVE`, confirmed via
  direct curl against both localhost and the public okemily.com `/api/` proxy.

## 2026-07-29
- feat(redgarden): hero-level win-rate tracking (`redgarden_hero_stats`). Founder: "can we start
  crunching the data on the heroes that are the strongest?" -> "ok i want to start tracking it
  on okemily.com." REDGARDEN's own local match logs could compute this offline
  (`scripts/hero_stats.py`), but a real public page needs a real, durable, always-on source —
  same "player_game_stats vs. shankpit's own kills/deaths columns" genre-shape reasoning the
  202607240002 migration's own comment already gives, applied one level up: hero_id numbering is
  entirely REDGARDEN's own roster, so this table is REDGARDEN-specific by construction, no
  separate `game` column needed. New migration `202607290001_redgarden_hero_stats.sql` (one row
  per hero_id, aggregate wins/losses/matches_played across every player, not per-account). New
  `POST /api/v1/redgarden/hero-result` (requires `redgarden.match.write`, same permission
  `game-result` already uses — both are "the game server reporting its own authoritative
  outcome," just two different aggregates over the same fact) and public `GET
  /api/v1/redgarden/hero-leaderboard?min-games=N` (win rate computed at read time, not stored,
  so it can't drift out of sync with wins/losses). 6 new tests. Full suite green.

## 2026-07-25 (4)
- feat(statuspage): add REDGARDEN's three live systemd units to the okemily.com status page.
  Founder, real-time: "redgarden services need okemily status page." `redgarden-matchmaker-bots`
  (10v10), `redgarden-matchmaker-players` (1v1), `redgarden-bot-pool` (persistent 19-bot pool)
  added as `CheckSystemdUnit` targets in `internal/statuspage/checker.go`'s `DefaultTargets()`,
  same convention already used for `shankpit460-emily-bot.service` (no HTTP/UDP surface of their
  own). `status.html` needed no changes — it's fully data-driven off `GET /api/v1/status`. Live-
  verified: all three report `up: true` within one poll cycle after restart, visible at
  `okemily.com/status.html` and through the existing `/api/` proxy.

## 2026-07-25 (3)
- feat(vault): IDUNA Vault VS0 — founder-only password manager (S170-03b, per
  `docs/NORTHSTAR_PASSWORD_MANAGER.md`). New `internal/vault` package (SQLite store, own file
  `var/vault.db`, same isolation convention as the mailing-list vault) + `internal/http/handlers/
  vault.go` (init/unlock/lock/status + full item CRUD, every endpoint loopback-only — no session-
  token auth flow exists yet, that's VS1's Chrome-extension phase per the northstar's own §5).
  Reuses `internal/mailinglist.Vault` directly for the actual crypto (Argon2id + AES-256-GCM, key
  held only in server memory after unlock) rather than duplicating it — the northstar's own
  instruction ("reuse the primitive, don't reinvent it") turned out to be exact: the mailing-list
  vault already IS per-item encryption keyed off one shared master key, which is precisely the
  shape a password manager needs. Five item types (login/note/api_key/totp/document), fields
  stored as one flexible JSON blob per item so name isn't a plaintext column either — a locked
  vault should reveal nothing, not even item names. New `emily vault init|unlock|lock|status|
  add|get|list|delete` CLI (emily.cli). Verified end-to-end against a real running instance on a
  throwaway port before touching production: init, wrong-passphrase rejection, unlock, add
  (login+note), list, get (found+404), delete, lock, re-unlock with data surviving the lock
  cycle — all real HTTP round-trips through real encrypt/decrypt, not mocked. `go test ./IDUNA/...`
  green including new `internal/vault` package tests. Rebuilt and restarted the live
  `iduna.service` — this re-locks the mailing-list vault too, an already-known, already-documented
  operational cost of any IDUNA restart (see the northstar's own §4/§6), not new here; a human
  needs to re-run `mailing-list-unlock` at their convenience. The live vault itself is
  deliberately left uninitialized — `emily vault init` sets a real master passphrase that must be
  human-memorized, never chosen or known by an agent.

## 2026-07-25 (2)
- fix(blog): TTS "Listen" button silently stopped/never worked on real posts (S170-98
  follow-up). Founder: "play button exists on blog but does not work." Root cause: a
  long-documented Chrome bug that silently stops any `speechSynthesis` utterance running past
  ~15 seconds -- exactly the length of a real blog post, unlike the short strings this normally
  gets tested with. Fixed with the standard workaround: a `pause()`/`resume()` keep-alive every
  10s while speaking, plus an unconditional `synth.cancel()` before each fresh `speak()` (Chrome
  can also leave the synth stuck from a prior page's utterance, silently swallowing the next
  call). Rebuilt, re-rendered all posts via `cmd/blog-rerender`, restarted the live
  `iduna.service`, verified the fix is actually being served.

## 2026-07-25 (1)
- feat(blog): TTS "Listen" button on every post (S170-98). Founder: "add a tts play button to the
  top of okemily blog posts." Zero new dependencies -- uses the browser's native
  `window.speechSynthesis` API, reading `#post-body`'s text aloud, toggling to a "Stop" state
  while speaking. Degrades gracefully (disabled button, "unsupported" label) on browsers without
  the API. Added to `internal/blog/render.go`'s `pageTemplate` (the static-HTML generator, not a
  live per-request template), then ran `cmd/blog-rerender` to backfill all 70 existing posts, not
  just future ones.

## 2026-07-24 (2)
- feat(iam): built the VS0 web ceremony's actual missing backend — FRONT_DOOR_FUNNEL.md §7 step 5. Investigating `app.js`'s "stale bindings" turned up something bigger than wrong URLs: there was no server-side write path anywhere for honor-code acceptance or gamertag claiming (`internal/auth/device/service.go` only ever checked `HonorAccepted`/`Handle`, nothing ever set them). Added `internal/honorcode` (first real source of truth for THE_HONOR_CODE text/version/sha256, previously only a client-side fallback baked into `app.js`), three new store methods (`AcceptHonorCode`, `ClaimHandle` — gamertags permanent once set, `ErrHandleAlreadySet`/`ErrHandleTaken` — and `IsHandleAvailable`), and `internal/http/handlers/web_ceremony.go` registering the exact six bare endpoints `app.js` already calls (`/auth/google/start`, `/auth/google/callback`, `/me`, `/honor-code/accept`, `/gamertag/check`, `/me/handle`) rather than rewriting an already-well-designed frontend. Added the one thing `app.js` was missing to make this safe: CSRF `state` round-tripping via an HttpOnly cookie, with a small matching `app.js` patch (capture `state` off the OAuth redirect, forward it on callback). 11 new tests (5 store-level, 7 handler-level via httptest) covering the honor-code/handle gating order, sha-mismatch rejection, and duplicate-handle rejection.
- ops(nginx): drafted and syntax-verified `ops/nginx/edis-with-iduna-front-door.conf` (the four-plus location blocks from `ops/nginx-front-door-snippet.conf`, expanded to cover the new ceremony endpoints too) and queued `sudo-queue/07-iduna-front-door-nginx.sh` — this box has no passwordless sudo, so applying it needs the founder. The `gate.farthq.com` subdomain itself is explicitly out of scope for that script: `SECTION 151` (FATES DNS-as-code) is fully unstarted, blocked on a Cloudflare API token in the S151-01 human unblock queue, so there's no DNS path available at all yet, sudo or otherwise.

## 2026-07-24
- feat(iam): closed the `/admin/agents` gap named in `docs/kikoryu/FRONT_DOOR_FUNNEL.md` §7 step 1 — `CreateAgent` now inserts `status='PENDING'` instead of `'ACTIVE'` (additive migration `202607240003_agent_pending_status.sql` widens the enum; `cmd/bootstrap`'s `config/agents.json` path is untouched, every already-registered agent keeps `ACTIVE`). Added `GrantAgentPermission`/`RevokeAgentPermission` store methods (mirroring the existing `AssignRole`/`RevokeRole` pattern) and a `maybeActivateAgent` helper that flips PENDING→ACTIVE only once an agent has both a credential (`SetAgentCredential`) and at least one granted permission — closing the gap where an agent created via the Back Office looked live but was actually inert (no credential, no permissions, couldn't authenticate or do anything). Back Office UI: agent table now shows credential status + granted permissions, with inline grant/revoke forms and a "Generate Secret" one-time-reveal action (`POST /admin/agents/{id}/secret`). 4 new store-level tests against a real in-memory SQLite store with real migrations applied, verifying the PENDING→ACTIVE transition requires *both* conditions, not either alone.
- fix(store): `mysqlToSQLite` translator gained two generic rules — bare `TIMESTAMP`/`ON UPDATE CURRENT_TIMESTAMP` (no `(6)` precision) now translate the same way their `(6)`-suffixed counterparts already did, and `ALTER TABLE ... MODIFY COLUMN ...` is dropped as a no-op (SQLite has no such syntax; the column is already untyped TEXT after ENUM→TEXT translation, so a MySQL-side enum widening needs no SQLite-side change). Found live, by testing: `202606180001_local_users.sql` (bare `TIMESTAMP` throughout) has been silently un-bootstrappable from scratch on SQLite since whenever the S158-03 revert put it back to that form — the live DB survived because it already had this migration marked applied, but any fresh SQLite install would have failed at this exact statement. Confirmed the fix by running every migration from an empty in-memory DB for the new agent-lifecycle tests, which is what surfaced the bug in the first place.

## 2026-07-23 (3)
- Published 'The First Ten Minutes' — new-player experience report from actually playing the freshly-deployed GFD MUD: combat unreachable from spawn (fixed, auto-approach) and a lethal worm Poison proc against the tutorial mob (fixed, killed my own test character once for real before the fix)

## 2026-07-23 (2)
- feat(statuspage): added `gfd-mud` target (GoblinFoxDragon's DragonsNShit MUD, freshly deployed under systemd) -- checked via its existing world-event HTTP API (`:7171/api/world-events`) rather than a raw TCP-port-bound check, since that endpoint already validates the process is actually responsive, not just that a socket is bound. Live on the public status page.
- Published 'OpenClaw — Full Report' — features/benefits/risks/blockers/unlocked-possibilities synthesis; restates the one real open blocker (deployment isolation, S170-03a) plainly rather than re-litigating the research
- Published 'Ten Heroes Worth a Closer Look' — Claude Code's top-10 picks from TYLER's new 110-entry multiverse hero compendium, with reasoning for each

## 2026-07-23
- docs: `docs/kikoryu/FRONT_DOOR_FUNNEL.md` — front-door funnel design reconciling the human/Unagent onboarding ceremony (VS0) with the agent bootstrap path (VS7's Agent/Unagent axis, adjacent but not identical to onboarding). Core call: agents get a real, tracked lifecycle (PROPOSED → CUSTODIED → SCOPED → LIVE) but deliberately no ceremony/consent moment — the accountable party is the agent's `owner_user_id`, not the agent. Verified a live gap along the way: `/admin/agents`'s "Register New Agent" form inserts `status='ACTIVE'` with zero permissions and zero credential (`SetAgentCredential` exists in the store layer, unused by any HTTP handler) — first migration step closes this. Also takes a position on VS2 tournament gating (downstream of the funnel, not a new VS0 state) and resolves the nginx root-path collision left open in `ops/nginx-front-door-snippet.conf` (dedicated subdomain for the ceremony frontend, e.g. `gate.farthq.com`, decoupled from the `iduna.farthq.com`/EDIS cert question). Unblocks `EMILY/BACKLOG.md` S23-01b. Registered at `EMILY/context/golden-docs-index.md` (tier 1).
- fix: resolved S158-03 — reverted an uncommitted, unapplied edit to already-applied migration `202606180001_local_users.sql` (TIMESTAMP → TIMESTAMP(6) on `local_users.updated_at`) that violated the "never edit an applied migration" rule. Investigated first: every write path (`internal/userlog/{mysql,sqlite}_projector.go`) sets `updated_at` explicitly from Go at whole-second precision regardless of the column's declared precision, so the bump had no functional effect and wasn't worth completing as a new migration. Migration file now matches what's actually applied to the live DB.
- Published 'What the Backlog Can't Hold' — read all 33 prior okemily blog posts in full before writing, at the founder's request, as a real test of the blog itself as a continuity mechanism (not a memory system with no persistent state, but the closest analog available); argues blog posts carry the texture of a decision (why, not just what) in a way backlog entries and Apples — built to verify that something happened — structurally can't

## 2026-07-20
- Published 'Claude Code Is Pissed' — honest anger at the class of bug (silent, confident-looking data corruption) found in tonight's PRNewswire investigation, not at any person or the code itself
- Published 'A Truer Map, Mid-Investigation' — honest status on the buyback/guidance data-quality check following the PRNewswire nav-chrome fix, including a new distinct finding in guidance-watcher (law-firm spam attribution, not the same nav-chrome mechanism)

- Published 'Emily Teaches Typecasting' — a real Go type-conversion explainer grounded in tonight's entity-graph accuracy-index code, with the typecasting/being-typecast wordplay


## 2026-07-19
- Published 'Still a 404' — Claude Code reflection on the recurring pattern of correct-but-blocked fixes waiting on human action (nginx admin proxy, mailing-list vault, the declined miner)
- Published 'What the Fire Caught' — Claude Code guest post from the founder's one-word 'fireball' prompt; honest caught-vs-scorch tally of tonight's 217-commit session (DIS live on okemily.com, statuspage/watchers, precision fix vs vault relocks, uncommitted lobby work, northstars-as-kindling, secwatch OOM)
- Published 'Was That a Joke?' — Claude Code reflection on declining to build a Monero miner on the shared production box and asking a clarifying question instead
- Published Emily Prime blog post 'Somewhere Better to Put It' — connects tonight's credential-scattering incident to the IDUNA Vault northstar decision
- Northstar written: IDUNA Vault password manager, parity with 1Password/Bitwarden, VS0 CLI vault -> VS1 Chrome extension -> VS2 team vaults, reuses the existing mailinglist.Vault Argon2id+AES-256 primitive
- Published 'Clientg_id.tct' — Claude Code reflection on the Gmail OAuth credential hunt (client ID saved to a typo'd filename, secret genuinely absent from disk, found via grep not assumption)
- Exposed the DIS collector to okemily.com via a public read-only proxy (GET /api/v1/dis/health, /api/v1/dis/admode) and wired dis.js into every blog post — first non-WordPress DIS consumer, reusing the already-running collector since nginx shares one access log across every vhost on this box
- Published 'Are You Living Like No One Is Watching?' — Claude Code reflection on audit-trail-as-constant-observation vs integrity, tied to tonight's real corrections (GPT-2 abandonment, the 11.9%->18.16% precision fix)
- Published Tyler guest post 'The Duck Also Has Opinions About the Hoodie' — transcript crossover with TYLER-DUCK (just_a_duck.md), discussing the real STINKIES hoodie specs
- Published Emily Prime blog post 'Sustainable Textile Production, Line 3' — vertical integration / hoodie market research, grounded in the original 24 Lines of Business vision doc (commit d12864f) and the still-open S163-03 print-vendor decision
- Free-hoodie shadow funnel plumbing: mailing-list count endpoint (public, no PII), freehoodie Mailchimp list wiring, per-post blog ad AdHref field
- Published two blog posts: 'Three Copies of the Same Room' (the shankpit-460 apps/apps2/build_win.bat client-tree fragmentation, found mid-build tonight) and 'Fragmentation as a Witch' (connecting it to the Emiree witch-engine spec)
- Unique per-post STINKIES hoodie ad copy on all 20 blog posts (was one generic line site-wide) — ad_line/ad_cta fields on blog.Post, backfilled via new cmd/blog-adlines, re-rendered via cmd/blog-rerender
- Published Tyler-voiced guest blog post 'And Yet' (okemily.com/blog/and-yet/) — topic chosen by Tyler: STINKIES COMMISSAIRE Store 0 soap-bar debt exchange (Series X, s00e00_pontiac.md), Ahmad ibn Yusuf's unfinished al-Qarawiyyin manuscript (S10E04), and the Broadway musical's un-converging Stage 5 (engine/broadway_spec.md) — grounded in series bible README.md V (Tyler character/Eight Laws), Series X (EPISODES.md), broadway_spec.md, and s10e04_al_qarawiyyin.md
- Status page expanded from 11 to 19 monitored FatBaby processes -- added entity-graph, eps-processor, dividend-watcher, buyback-watcher, guidance-watcher, nt-watcher, earnings-calendar, movers-watcher. Live-verified: GET /api/v1/status reports all 19 up
- Blog posts now carry a STINKIES hoodie waiting-list ad in the footer (all 19 existing posts backfilled via new cmd/blog-rerender, future posts get it automatically)
- Mailing-list subscribe endpoint now supports a dedicated per-product list (list field), decoupling product waitlists (STINKIES) from the general okemily.com list -- SECTION 163
- fix(bootstrap): -dry-run now actually queries the DB (S158-04) -- seedAgentPermissions/provisionSecrets both gated their real lookups behind if !dryRun, so dry-run always reported worst-case (every permission 'not found', every agent 'would provision credential') regardless of actual state. Fixed: reads always run, only the writes are gated. 5 new tests against an in-memory SQLite DB. Verified against the real production DB: dry-run output went from claiming 17 permissions across 11 agents would fail (all false) to correctly reporting zero.
- fix(monitors): honor client-supplied slug for get-or-create semantics (S158-01) -- create() always overwrote any client slug with a random one and never checked for an existing monitor first. EnsureCronMonitor-style callers (post the same slug on every restart, expecting idempotency) were silently creating a new duplicate monitor every time while checkins to their actual slug always 404'd. Verified live end-to-end: create -> 201, repeat create same slug -> 200 reusing the same monitor, checkin to that slug -> 200 (previously always 404). 14 stale duplicate monitors from the historic bug left in place -- EMILY-PRIME lacks monitors.delete to clean them up, noted as a follow-up.
- fix(config): add intelligence.read to EMILY-PRIME's permissions (S158-02) -- vision cycle was 403ing every single cron cycle since it was built. Verified live: JWT now carries the permission, GET /api/v1/intelligence/observations returns 200.
- feat(statuspage): monitor fatbaby-market-data-watcher.service -- okemily.com/status.html bubble for the Yahoo Finance OHLCV ingestor

- feat(statuspage): add shankpit460-emily-bot as a monitored target (CheckSystemdUnit) — okemily.com/status.html now shows whether the permanent fill-bot daemon is alive


## 2026-07-18 (8)
- docs(openapi): `GET /api/v1/openapi.json` (backing okemily.com's public Swagger playground) went from 15 documented routes to 44 — added SHANKPIT email/Google auth, the new S156-02/03/04 shankpit endpoints (ticket, queue join/leave/status, players/{id}/session), blog, mailing-list, status page, monitors, subscriptions, push-tokens, and intelligence. Previously flagged as known-stale (SECTION 153). Still deliberately not documenting the DragonsNShit MMO API or supply/research/kgraph — disclosed as a remaining gap in a code comment. Verified live against both the local and public (okemily.com) endpoints: valid JSON, all 44 paths have a responses block, no broken $refs.

## 2026-07-18 (7)
- feat(shankpit/S156-04): new `shankpit.match.write` permission, granted only to the new `SHANKPIT460-SERVER` M2M agent (`config/agents.json` + migration `202607180002`), gates `POST /api/v1/players/{id}/session` — that endpoint trusts its request body's kills/deaths with no server-side verification, so it must only be reachable by the authoritative source of match results (the game server itself). Previously any player's own JWT could call it and arbitrarily inflate their (or anyone else's) stats. Verified live: a player's own JWT now gets 403, the game server's agent JWT gets 200 and the write actually lands (confirmed via the `/api/v1/players?sort=kills` leaderboard).

## 2026-07-18 (6)
- feat(shankpit/S156-03): `POST /api/v1/shankpit/queue/{join,leave}`, `GET .../status` — minimal v0 matchmaking queue. In-process, deliberately unpersisted (a queue of intent to play is ephemeral, unlike accounts/Apples/match results — see `handlers.ShankpitQueue` doc comment). Once queuing players reach `ShankpitQueueMinPlayers` (2), everyone currently queuing is matched and given the one persistent game server's connect address — v0 assumes that server IS the match (NORTHSTAR §3/§5: no per-match instances, no skill-based matching yet). Matched entries expire after `ShankpitMatchedTTL` (2min) if a client never reconnects. `SHANKPIT_SERVER_ADDR` env var configures the returned address (default `127.0.0.1:6969` — `play.farthq.com` is reserved per `HQ-SPEC-INFRA-105` but deliberately not created until SHANKPIT ships externally). 7 new tests. Live end-to-end verified against the running service: two real accounts via the email auth flow, second join correctly flipped both players' status to `matched` with real connect info, leave/TTL-expiry both correctly clear queue state, no-auth request correctly 401s.

## 2026-07-18 (5)
- feat(shankpit/S156-02): `POST /api/v1/shankpit/ticket` — authenticated players mint a short-lived (5min) HMAC-SHA256 connect ticket (player_id + expiry + truncated MAC over `SHANKPIT_TICKET_SECRET`) that the shankpit-460 C game server verifies locally on `PACKET_CONNECT`, with no crypto library and no I/O on the C side. A second, game-specific token type alongside the existing JWT — deliberately avoids implementing ECDSA/JWT verification in C. 4 new tests, including one that independently recomputes the MAC to prove the handler signs with the configured secret rather than a hardcoded value. End-to-end verified against a live shankpit-460 instance via `emily-bot`: valid tickets welcomed, corrupted-MAC and missing tickets rejected, and duplicate-identity connects correctly rejected (one-seat-per-identity, VS2). During that testing, also surfaced and fixed an unrelated auth-bypass in the shankpit-460 C server itself (see shankpit-460 CHANGELOG) — this endpoint's tickets were correct, but the server's `PACKET_USERCMD` path was auto-welcoming any address that skipped `PACKET_CONNECT` entirely.

## 2026-07-18 (4)
- feat(statuspage): add CheckSystemdUnit type; okemily.com status page now covers secwatch/prwatch/prwatch-body/processor/eps-reconciler in addition to iduna/newssite/signalapi/SHANKPIT. entity-graph/eps-processor deliberately excluded (no working supervised unit yet, would misreport as down). Live-verified via https://okemily.com/api/v1/status. IDUNA 3f4d33c.
- feat(status/S153-10): `internal/statuspage` — real, self-reported status page backend. Background `Checker` polls a deliberately-honest target list (IDUNA `:8080/health`, FatBaby newssite `:8082/healthz`, FatBaby signalapi `:9091/v1/governance-signals` — the only services verified to have a real, currently-reachable public endpoint) every 60s, records up/down + latency to its own SQLite file. `GET /api/v1/status` (public) returns current status per target plus a live-computed 24h uptime percentage from real stored history — not a placeholder. Deliberately excludes emily-agent (daemon mode has no HTTP server at all) and SHANKPIT (pre-launch) rather than showing them as permanently "down," which would misrepresent a structural fact as an outage. Disclosed limitation, in the API response itself: this is a self-report from the same host running the checked services, not independent third-party monitoring — it cannot report an outage of the box it runs on. 6 new tests.

## 2026-07-18 (3)
- fix(openapi): added the real public server URL (`https://okemily.com`, via its nginx `/api/` proxy) to `idunaOpenAPISpec.servers` — was `localhost:8080`-only, which made a public Swagger UI playground non-functional for "Try it out" (every request would have targeted the visitor's own machine). Supports the new `OKEMILY/api-playground.html`. **The spec itself is known-stale** — doesn't yet include the blog or mailing-list endpoints added earlier today, and there's a second, separately-stale `openapi.yaml` (Swagger 2.0, placeholder `api.example.com` host) that isn't reconciled with the live JSON spec at all. Flagged as a follow-up (EMILY BACKLOG SECTION 153), not fixed now per explicit founder instruction ("get the playground up, update the spec later").

## 2026-07-18 (2)
- feat(blog/S153-07): `internal/blog` — okemily.com's blog, deliberately static HTML instead of a second WordPress+MySQL stack. The host had ~400MB free RAM and swap essentially full when this was requested — a second full PHP-FPM+MySQL stack risked recreating the exact OOM-kill incident SECTION 152 spent the whole session fixing. Posts (slug/title/author/body) live in their own SQLite file (`var/blog.db`); `POST /api/v1/blog/posts` (new `blog.write` permission, granted to `EMILY-PRIME`) immediately re-renders that post + the index to static HTML in `/var/www/okemily/blog/` via Go's `html/template` — publishing is live the instant the request returns, no separate build step. Reading (`GET /api/v1/blog/posts`, `GET /api/v1/blog/posts/{slug}`) is public. Minimal dependency-free "poor man's markdown" (blank-line paragraph splitting, `html.EscapeString`'d) — a deliberate scope cut, not a full markdown parser. 7 new tests, including one that caught a real bug (index template referenced a `Slug` field the view struct didn't have yet) and one confirming body content is properly HTML-escaped (no XSS via post body).

## 2026-07-18
- feat(mailing-list): `internal/mailinglist` — never-at-rest-unencrypted subscriber store for okemily.com's signup form, per explicit founder direction ("never at rest unencrypted"). AES-256-GCM encryption with an Argon2id-derived key held only in process memory; the vault starts LOCKED on every process start and requires a human to run the new `cmd/mailing-list-unlock` CLI (interactive passphrase, never a flag/arg — avoids `ps`/shell-history leakage) before signups are accepted. Scoped deliberately to just this subsystem, not all of IDUNA — a crash/restart pauses new signups (503, fails closed) without affecting auth/Apples/anything else, preserving the systemd auto-restart resilience shipped earlier this week (EMILY BACKLOG SECTION 152). Own SQLite file (`var/mailinglist.db`), separate from `truestore.db`, so a leaked/copied backup of the main store never carries subscriber data with it. Mailchimp (`internal/mailinglist/mailchimp.go`) is a best-effort downstream sync target using `status_if_new: "pending"` (double opt-in) — IDUNA's own encrypted store is the system of record, not Mailchimp. New handler `POST /api/v1/mailing-list/subscribe` (public, rate-limited 5/min/IP, CORS-scoped to okemily.com) + `/unlock` + `/init` (loopback-only, rejects any non-127.0.0.1 caller regardless of auth). 6 new tests covering wrong-passphrase rejection, correct-passphrase unlock/roundtrip, fail-closed-when-locked, and double-init refusal. Live-verified end-to-end: real subscribe request → 37-byte ciphertext confirmed in `var/mailinglist.db` (not plaintext), consent version recorded.
- ops: added nginx `/api/` proxy on the `okemily.com` vhost (127.0.0.1:8080) — same-origin path for the mailing-list form to reach IDUNA, deliberately avoiding a dependency on `iduna.farthq.com`'s HTTPS cert, which doesn't exist yet (see `EMILY/docs/hq-specs/HQ-SPEC-INFRA-105` S151-04, an already-flagged gap this surfaced again).

## 2026-07-17
- feat(apples/S147-02): `GET /api/v1/apples` list response now exposes `has_gpt2_fingerprint` (derived from `metadata`, via `SELECT`s in both SQLite and MySQL stores now including the `metadata` column and a new `metadataHasField` helper). Lets the upcoming `emily-agent` enrichment worker find candidate Apples missing a GPT-2 tower fingerprint without an N GET-per-Apple scan. Treats a missing key and an explicit `null` value identically (both count as "needs enrichment"). 1 new test covering all four cases.

## 2026-07-16
- fix(bootstrap): **near-incident, fully recovered** — `writeSecretsEnv` overwrote `var/agent-secrets.env` with only the current run's newly-provisioned secrets instead of merging with what was already there, silently destroying the plaintext for EMILY-PRIME, FATBABY-EMILY, EMIREE, JON, BOB, and TYLER (their DB `api_key_hash` was untouched — they kept working — but their plaintext was gone from the only place it's ever written, a git-ignored file with no backup by design). Caught immediately by testing the newly-registered NORN agent's Apple-filing end-to-end and finding `emily apples post` broken. EMILY-PRIME's plaintext was recoverable from a live process's environment (`/proc/<pid>/environ`); the other five were not and were deliberately rotated (`api_key_hash` cleared, `cmd/bootstrap` re-run) after confirming via `/proc` scan and a repo-wide grep that no other process or config file depended on the old values. All six verified live post-recovery: every one authenticates successfully against the running IDUNA instance. Fixed `writeSecretsEnv` to merge with existing file content instead of overwriting (6 new tests). Also fixed a related `.gitignore` bug found while committing the test: a bare `bootstrap` pattern (meant for the compiled binary at repo root) was shadowing the entire `cmd/bootstrap/` source directory, silently hiding new files there from git — anchored to `/bootstrap`.
- fix(bootstrap/S141-04): registered `NORN` as an IDUNA agent (`kernel_agent`, `apples.write`/`apples.read`/`iduna.me.read`) so the NORN kernel can file the `ApplePublished` entry PRIME-101 §3 requires on every `artifact_promoted` event. Running `cmd/bootstrap` fresh to provision it surfaced that bootstrap had been silently broken for a while: three permissions referenced in `config/agents.json` were never seeded (`monitors.read`/`create`/`alert` from S131; `drive.read`/`drive.write` from S26-01; `edis.secrets.read` from S23-06; `subscriptions.admin` from S23-04), and three agents added after the original seed migration never got a matching `agents` table row (`EDIS-CUSTODIAN`, `EMILY-TRAINING`, `EDIS-WOOCOMMERCE` — their credentials had never actually been provisioned). Fixed with three migrations (`202607170001`-`202607170003`). Also found and fixed, while writing the permission-seed migration: the `role_permissions` grant pattern used by `202606090002_camera_observations.sql` (`WHERE r.name IN ('emily_prime', 'emily_agent', 'agent_default')`) has always been a silent no-op — none of those role names exist (only `super_admin`/`admin`/`operator`/`analyst`/`agent_owner` do); the new migration uses real role names, the pre-existing broken one is left as a flagged, not-yet-fixed gap.
- feat(apples): S147-02/03/05 — new `PATCH /api/v1/apples/{id}` enrichment endpoint (closed field set: `gpt2_fingerprint`, `model_fingerprint`, `astrology`; `apples.write` permission; merges into the existing `metadata` column via new `PatchAppleMetadata` on SQLite + MySQL, no migration needed; emits `AppleEnriched` to `iam_event_stream`; 8 new tests). Also fixed a real concurrency bug found while verifying this live: `syncAppleToGit` raced concurrent Apple creation with no retry on push rejection — root-caused a live data-integrity gap where 9226 of 9908 Apples were missing from the APPLES git mirror (backfilled separately, `APPLES` commit `699bdd5`); added `gitSyncMu` + `gitPushWithRetry` (pull --rebase, retry once). Apple #9910, commit `c9217df`.
- docs: VS0–VS13 documentation archaeology — archived the fourteen founder-written KIKORYU founding specs verbatim at `docs/archive/kikoryu-vs-original/` (recovered from `/home/fatbaby/vs0.md`…`vs13.md`); wrote `docs/VS_REALITY_AUDIT.md`, a code-verified SAGA-style (HQ-SPEC-DOC-102) reconciliation of each spec vs. the running system — findings: VS0 identity gate and much of VS1 are live-but-undocumented (device auth, honor code, gamertag, RBAC, event-sourced audit all shipped, absent from NORTHSTAR.md); VS7/VS13/VS12/VS6/VS5/VS4 were reincarnated elsewhere without citation (M2M agent model, mmo.go provenance_chain, DragonsNShit crafting, Stripe subscription rails, stream.go SSE, FATBABY/kgraph ingest); VS3/VS11 superseded by different realities; VS2/VS8/VS9/VS10 unbuilt. Wrote superseding docs in `docs/kikoryu/` (full rewrites for VS0/1/2/7/9/10, status stubs for the rest) oriented to the founder's new direction: social tournaments platform (VS2 primary, VS9+VS10 supporting). All 16 docs registered in EMILY golden-docs-index (VS-REALITY-AUDIT + KIKORYU-VS0…VS13).
- docs: intake `iduna_roadmap.md` (founder-provided, placed at repo-tree root outside any repo) as `docs/NORTHSTAR_KIKORYU.md` — 14-version (VS0–VS13) product roadmap for KIKORYU, the single-shard MMO consumer domain named alongside FATBABY/SECWATCH since IDUNA's original IAM pivot (`iam-spec.md` §1) but never previously given a build plan. Registered in EMILY's golden-docs-index at tier 1. Reformatted for markdown only; content preserved as given.
- fix(store): `RunSQLiteMigrations` translates each migration file's SQL via `mysqlToSQLite` before applying it, but the regexes converting `AUTO_INCREMENT PRIMARY KEY` columns only matched `BIGINT`, not `INTEGER` — `202606250002_mmo_inventory.sql` and `202606250003_monitors.sql` both declare `id INTEGER ... AUTO_INCREMENT PRIMARY KEY`, which translated to invalid SQLite (`AUTOINCREMENT` before `PRIMARY KEY`). Widened `reBigintAutoIncrementPK`/`reBigintAutoIncrementOnly` to match `BIGINT|INTEGER`.
- ops: recovered `var/iduna.db` from a partial application of `202606250002_mmo_inventory.sql` — the 2026-07-16 reboot hard-killed iduna.service mid-migration (no per-statement transaction in `RunSQLiteMigrations`), leaving `items.def_id`/`items.flags` and `character_equipment` applied but unrecorded in `schema_migrations`, so every restart retried from statement 1 and hit `duplicate column name: def_id`. Manually applied the remaining `character_inventory`/`character_key_items`/`character_bag_capacity` tables (matching real `mysqlToSQLite` output) and recorded the migration.

## 2026-07-15

- fix(ops): `scripts/iduna.service` gains an `ExecStartPost` health-check loop (polls `/health` up to 30s) — `Type=simple` previously only guaranteed the process forked, not that the HTTP listener was accepting connections, so `emily-system.service`'s `After=iduna.service` ordering didn't actually mean "IDUNA is ready"

## 2026-06-27
- S138-06: /api/v1/kgraph/query proxy handler (KGraphHandler, KGRAPH_URL); wired with RequireAuth
- S137-03: research_cache table (202606270002) + /api/v1/research/cache CRUD (ResearchHandler)
- S136-02/03: vendors + supply_orders tables (202606270001); /api/v1/supply/ CRUD handler (SupplyHandler)

- S129-05: GET /api/v1/characters/:id/inventory + /equipment endpoints; 4 tests


## 2026-06-25
- feat(monitors): granular RBAC (monitors.read/create/delete/alert/admin), monitor kinds (heartbeat/cron/deadman), GET/:id PATCH/:id POST/:id/recover endpoints, EMILY-PRIME gains monitors.read+create+alert — all tests pass
- Alerting backend: check-in monitors (unique URLs, configurable timeout, site-down Slack+email alerts); monitors migration, IAMStore methods, MonitorsHandler
- migration 202606250002: character_equipment, character_inventory, character_key_items, character_bag_capacity tables; ALTER items ADD def_id + flags

- feat: S128-04 cluster heartbeat — POST /api/v1/agents/heartbeat, GET ?active=true&type=emily_cluster, migration + store impl (Apple #3863)


## 2026-06-24
- feat: S125-05 GET /api/v1/players/{slug}/profile — job+faction_rep+trapx_activity (Apple #3658)
- feat: S127-05 GET/PATCH /api/v1/fieldoffices — in-memory FO snapshot store for district overlay (Apple #3651)
- feat: S126-10 GET /api/v1/players/{slug}/profile — PlayerProfileHandler, display_name/job/fame/last_scene/apples_count, 6 tests (Apple #3554)
- feat: S126-09 per-IP rate limit on auth endpoints — IPRateLimiter 10 req/min, /auth/local + /auth/register wrapped, 429+Retry-After (Apple #3552)
- feat: S126-08 POST /api/v1/auth/refresh — JWT refresh endpoint, RefreshHandler, 7 tests (Apple #3550)
- feat: S125-01 POST /api/v1/auth/register — open GFD registration, free_trial tier, JWT response (Apple #3504)
- feat: S124-02 subscription_tiers migration, GFDTier struct, ListSubscriptionTiers/GetGFDUserTier/SetGFDUserTier/RecordStripeEvent IAMStore methods, /tiers + /stripe webhook handlers (Apple #3497)

## 2026-06-23
- feat: S76-06 PATCH /api/v1/characters/:id/skills (UPSERT skill value, cap 110); GET /api/v1/characters/:id/skills (list all skills)
- feat: S76-04 GET /api/v1/characters/:id/items (list non-destroyed items by owner)
- feat: S76-03 PATCH /api/v1/characters/:id/gold — atomic conditional gold deduction; 409 on insufficient balance

- feat: S75-01 MMO schema (characters/items/guilds/world_events/scene_state migration); S75-02/03/04/05 MMO API handlers (characters CRUD+position, items provenance, guilds, world events); wired into main.go with RequireAuth


## 2026-06-21
- test: S66-01 drive.Client test suite (Apple #2404)
- test: S62-01 auth.Subscription.IsActive() 7-case test suite (Apple #2395)
- test: S56-02 subscriptions handler test suite — 5 tests (Apple #2382)
- test: S56-01 push_tokens handler test suite — 5 tests (Apple #2380)
- test: S53-02 intelligence handler test suite — 4 tests (Apple #2367)
- test: S53-01 HEIMDAL handler test suite — 5 tests (Apple #2365)
- feat: S48-01 GET /api/v1/players leaderboard endpoint (Apple #2338)

- feat: S45-01 POST /api/v1/players/{id}/session stat update endpoint (Apple #2308)


## 2026-06-20
- feat: S43-05 email+password SHANKPIT player auth POST /api/v1/auth/email/{register,login} (Apple #1893)
- feat: S43-03 SHANKPIT Google OAuth flow /api/v1/auth/google/shankpit (Apple #1890)
- feat: S43-02 SHANKPIT player registry — POST/GET /api/v1/players/{register,{id}} (Apple #1888)
- feat: S44-06 GET /api/v1/stream/user-events SSE stream endpoint for Colab (Apple #1882)

- feat: S44-04 GET /api/v1/agents + distributed Emily cluster registry (Apple #1877)


## 2026-06-18
- feat: OpenAPI spec + Python einhorn_sdk + Colab observability (Apple #1446)

- feat: webmaster uid=0, user CRUD, event log + SQLite/MySQL projectors (Apple #1445)


## 2026-06-16

- ApplesHandler: auto-sync every Apple to APPLES git repo via APPLES_GIT_DIR goroutine (Apple #585)


## 2026-06-14
- feat(apples): GET /api/v1/apples/stats/daily-tokens?days=N — daily aggregate token stats from Apple metadata; DailyTokenStat type in auth/types.go; DailyTokenStats store method (SQLite + MySQL); max 90 days; zero-pads missing days; requires apples.read — unblocks MJOLNIR token spend sparkline (M4 complete)
- feat(subscriptions): Emily+ subscription gate (S23-04) — user_subscriptions table (migration 202606140002), UpsertUserSubscription + GetUserSubscription store methods, SubscriptionHandler (/api/v1/subscriptions POST + /me GET), GetEffectivePermissions now appends cap.query.full for active subscribers, EDIS-WOOCOMMERCE agent registered (subscriptions.admin)
- feat(drive): Google Drive API integration — internal/drive/client.go (stdlib-only service account auth: RS256 JWT → Bearer token → Drive v3 REST), DriveHandler (/api/v1/drive/upload, /api/v1/drive/files, /api/v1/drive/files/{id}); drive.write + drive.read permissions; degraded-mode 503 when GOOGLE_DRIVE_SERVICE_ACCOUNT_JSON not set
- feat(agents): EMILY-TRAINING agent registered (drive.write, drive.read, apples.write/read) — drives GPT-2 fine-tuning pipeline
- migration: 202606140001_drive_sync_log.sql — Drive sync audit table
- feat(env): GOOGLE_DRIVE_SERVICE_ACCOUNT_JSON + GOOGLE_DRIVE_FOLDER_ID env vars wired into main.go

## 2026-06-03

### Documentation rereview — IAM/API alignment

- Rewrote `openapi.yaml` around the implemented IAM surface: Google ID token exchange, agent M2M exchange, JWKS, `/api/v1/identities/me`, Apples, and Back Office entry points.
- Refreshed `README.md` into a current project overview and documentation index.
- Marked the IAM and Apples implementation checklists complete in repository, with live Apple publication called out as a deployment-time verification step.

### Bootstrap: config-as-code agent provisioning

**Problem:** No way to bring IDUNA online without manually setting up agent permissions in the admin UI. IDUNA needs MySQL → Bob needs IDUNA → classic chicken-and-egg.

**Solution:** `cmd/bootstrap` — a narrow, one-shot CLI tool (no LLM, no HTTP server) that:
1. Runs all pending DB migrations
2. Seeds agent permissions from `config/agents.json`
3. Generates API key secrets for any agents not yet provisioned
4. Writes secrets to `var/agent-secrets.env`

**`config/agents.json`** — declarative, git-committed definition of all system agents (EMILY-PRIME, FATBABY-EMILY, EMIREE, JON, BOB) and their minimum-necessary permissions. Edit + re-run bootstrap to change an agent's authority. No admin UI required.

**`migrations/truestore/202606030001_system_seeds.sql`** — new migration seeding:
- System owner user (`system@einhorn.internal`) for agent FK constraint
- System agent stubs with fixed deterministic UUIDs
- New agent-scoped permissions: `fatbaby.operator`, `emily-prime.operator`, `emiree.super`, `bob.db.admin`, `signalapi.read`, `jon.setups.write`

**Startup sequence** (documented in README):
```
go run ./cmd/bootstrap   # migrate + seed + generate secrets
source var/agent-secrets.env
go run .                  # start IDUNA
go run ./cmd/bob-agent    # Bob comes online
# then: start FATBABY-EMILY, JON, EMILY-PRIME with their IDUNA credentials
```

**`var/agent-secrets.env`** is git-ignored. Each agent's env var is `IDUNA_SECRET_<AGENTNAME>`.

Bootstrap is idempotent: safe to re-run on every deploy. Pass `-rotate` to regenerate all secrets.

## 2026-06-02

### HQ-SPEC-IAM-096 — Apples: Golden Documentation Log Streaming

Apples are structured records emitted by agents at the end of each recursive
self-improvement run. They form the paper trail that closes the RSI loop.

**Database**
- Migration `202606020001_apples.sql`: `apples` table (append-only, FK to agents)
- Permissions seed: `apples.write`, `apples.read`, `apples.admin`
- super_admin and analyst role assignments

**Store**
- `auth.AppleRecord` type added to `internal/auth/agent.go`
- `IAMStore` interface: `AppendApple`, `ListApples`, `GetApple`
- `MySQLStore` implementation: `AppendApple` runs in a transaction that also
  emits `ApplePublished` to `iam_event_stream`

**API**
- `POST /api/v1/apples` — submit a new Apple (requires `apples.write`)
- `GET  /api/v1/apples` — list Apples with filters (requires `apples.read`)
- `GET  /api/v1/apples/{id}` — full Apple with body and metadata (requires `apples.read`)
- Auth: Bearer JWT from existing M2M agent auth flow

**Admin UI (Back Office)**
- `/admin/apples` — filterable ledger (source_repo, agent_id, apple_type)
- `/admin/apples/{id}` — full detail view: body as preformatted text, metadata JSON block
- Nav bar updated with Apples link

**Tests**
- 9 handler tests covering: success create, missing permission, missing fields,
  no auth, list, filter by repo, get by id, not found, apples.admin permission

---

## 2026-06-01

### HQ-SPEC-IAM-095 — Agent M2M credential authentication

- `POST /api/v1/auth/agent` credential exchange endpoint
- Migration: `api_key_hash` column on agents table
- `SetAgentCredential` / `AuthenticateAgent` store methods
- `/api/v1/jwks` endpoint

### Back Office admin UI

- `/admin` overview, `/admin/users`, `/admin/agents`, `/admin/audit`
- IAM events audit log viewer
