# Emily for Business — IDUNA as a Zero-Trust, Agent-Native IAM Product

*Real product-scoping pass, 2026-09-03 (S243-02, EMILY/BACKLOG.md's own cruise-column
sprint-planning). Not an implementation plan — a grounded input for a founder-level product
decision. See this doc's own "Open questions for the founder" section for what this does NOT
resolve.*

## Where this came from

Founder's own framing, kanban cruise queue, preserved verbatim: "IDUNA IS THE PRODUCT BASICALLY
ZERO TRUST SECURITY AGENT NATIVE." No NORTHSTAR existed for this idea anywhere in the monorepo
before this document (checked: no `EMILY_ENTERPRISE`/"emily for business" doc in EMILY, IDUNA,
or EmilyOS).

## Real, direct tension this doc has to name, not smooth over

`IDUNA/docs/NORTHSTAR.md`'s own current framing, written 2026-06-13 and still standing: **"IDUNA
is not a product. It is the backbone."** That line was correct for what IDUNA was built to be —
the internal trust authority every other repo in this monorepo authenticates through. The
founder's new framing is a real, explicit reversal of that stance, not a refinement of it. This
document does not silently resolve that tension in either direction; it hands both framings to
the founder for a real decision (see "Open questions" below), and describes what's actually true
of IDUNA today under both readings.

## What IDUNA actually has today — checked directly, not assumed

A real, working internal IAM backbone, already zero-trust-*flavored* in several concrete ways:

- **ES256 JWT + Google OAuth (humans) + M2M agent credentials (agents), same trust root.** No
  consumer service in this monorepo trusts a token from anywhere but IDUNA — every JWT is
  verified against IDUNA's own published key set (`/.well-known/jwks.json`).
- **Agent-native from day one, not bolted on.** `POST /api/v1/auth/agent` (`agent_name` +
  `agent_secret` → JWT with explicit `permissions[]`, no role inheritance) is a first-class,
  equally-supported auth path alongside human OAuth — most commercial IAM products treat
  machine/service identity as an afterthought retrofitted onto a human-first model. This is a
  real, honest differentiator if "agent native" is the pitch.
  Hierarchical RBAC (capabilities flow resource → role → user/agent).
- **Append-only Apples audit ledger**, git-backed (`APPLES/` repo, `emily sync
  --apples-git-dir`) — a genuinely tamper-evident record of every meaningful system event,
  queryable by type/repo/agent/date.
- **Unified logging backend** (`EMILY/BACKLOG.md` SECTION 226, closed 2026-09-03): a real,
  Splunk-HEC-shaped ingest (`POST /services/collector`) and search (`GET
  /services/search/jobs`) layer, with real emission points across every login surface, admin
  role/permission/secret-rotation action, HEIMDAL sprint transitions, and Apple creation. "One
  real place to jump to and grab the logs" (founder's own framing when this shipped) is a real,
  sellable security-operations story on its own.
- **Least-privilege, auditable admin actions**: role assign/revoke, agent permission
  grant/revoke, and agent secret rotation are all individually logged (the last one records that
  a rotation happened, never the plaintext secret — verified live by extracting the actual
  generated secret and confirming it never appears in the event payload).

## What a real "zero trust" pitch to an external buyer would need that doesn't exist yet

Named honestly, not glossed over — this is the actual gap between "internal backbone with
zero-trust-flavored properties" and "a sellable zero-trust product":

1. **No multi-tenancy.** IDUNA is single-tenant today — one `agents.json`, one set of
   roles/users, one database. Selling this to external customers means real tenant isolation
   (data, credentials, audit logs all scoped per customer), not a small feature, a real
   architectural change.
2. **No self-serve onboarding.** Agents are seeded via `cmd/bootstrap` reading a hand-edited
   `config/agents.json` — there is no signup flow, no customer-facing agent-provisioning API or
   UI.
3. **No continuous device/posture verification.** A real "zero trust" architecture (per NIST
   SP 800-207, the standard reference model) expects continuous, per-request trust evaluation —
   not just "issue a JWT, trust it until it expires." `EmilyOS` (a separate, related repo) has a
   real *design* for exactly this (`docs/POSTURE.md`'s posture state machine: NORMAL/SIEGE/
   MERCY/INCIDENT/GAME) — but its own milestone markers are still mostly `[ ] Milestone 1/2`,
   i.e. largely unimplemented. IDUNA and EmilyOS are not integrated today; whether "IDUNA is the
   product" means folding EmilyOS's posture model in, or leaving IDUNA narrower than EmilyOS's
   own real zero-trust ambition, is itself an open question below.
4. **No network micro-segmentation story.** JWT-based service-to-service auth is one real leg of
   zero trust (identity-based access); nothing here today makes claims about network-layer
   segmentation, which most real zero-trust product pitches also cover.
5. **No per-customer billing/quota/rate-limit story.** Needed for literally any external SaaS
   product, not specific to zero trust, and not built.
6. **No compliance attestation.** EmilyOS's own `docs/SOC2.md` is a real, honest *controls map*
   (not an attestation) — explicitly states "SOC 2 is an attestation about controls. We do not
   claim 'unhackable.'" — and its own control statuses are mostly `[ ] Milestone 2`. A real
   external security-product pitch usually needs a real completed audit, not a design doc, before
   enterprise buyers take it seriously.

## Real, honest bottom line

IDUNA is a real, working, internally-proven, agent-native IAM backbone with several genuinely
sellable properties (agent-first identity, tamper-evident audit trail, unified log
search/ingest). It is **not**, today, a productizable external "zero trust security" offering —
the gap is multi-tenancy, self-serve onboarding, continuous posture verification, and compliance
attestation, none of which are small. This is a real, substantial build if pursued, not a
repackaging exercise.

## Open questions for the founder — not resolved here, by design

1. **Is "IDUNA is the product" a literal instruction to externalize the CURRENT IDUNA**, or a
   direction to build a new, separate product (working name "Emily for Business") that reuses
   IDUNA's real internal design/code as a starting point while IDUNA itself stays the internal
   backbone? These have very different engineering paths (retrofit multi-tenancy into a running
   system everything else depends on, vs. fork/extract a new service).
2. **Who is the buyer?** Enterprises evaluating IAM vendors broadly, or other teams building
   agentic AI systems who specifically need "agent-native" identity (a narrower, newer market
   with less established competition)? The pitch and the roadmap differ substantially.
3. **Does EmilyOS's posture-kernel design get folded into this pitch**, or does "zero trust" here
   mean something narrower (identity + audit, the parts that are actually real today) than
   EmilyOS's own full continuous-verification ambition?
4. **Pricing/packaging model** — not attempted here at all.

## Real next step

A real founder-level product-scoping conversation, informed by this document's own "what's real
today" section — not attempted as an engineering task in this pass. Once that conversation
resolves question 1 above in particular, the actual engineering sequencing (multi-tenancy first?
self-serve onboarding first? EmilyOS integration first?) can be scoped for real.

## Multi-tenancy architecture direction (2026-09-03, founder real-time — partially answers Open
Question 1 above)

Founder, real-time, preserved verbatim: "so to make iduna multi tenant we need like a DB per
install and we need console.okemily.com for our customers and partners to onboard also we can
use the fatbaby proxies for offering custom subdomains for partners/customers." This is real
direction on the mechanism (DB-per-install + a real onboarding portal + subdomain routing) — it
does not by itself resolve whether this is IDUNA-externalized-in-place or a separate product
(Open Question 1 is still open), but the mechanism described works identically either way, so
it's worth grounding now.

**Real, checked finding: DB-per-install is already close to free.** IDUNA's own `main.go`
resolves its database from a single, per-process `SQLITE_PATH` (or `MYSQL_DSN` for the MySQL
backend) env var, and `RunSQLiteMigrations` runs cleanly against a fresh, empty file. A
"DB-per-install" tenant is structurally just: a new `SQLITE_PATH`, a fresh migration run, a new
`agents.json` seeded via `cmd/bootstrap`, on a distinct port. The real work here isn't the
database layer itself (already tenant-shaped by accident of how it was built) — it's
**orchestration**: something has to actually spin up a new IDUNA process (or container) per
tenant, allocate it a port, and register it. Not built yet, not attempted in this pass.

**Real, checked finding: `console.okemily.com` doesn't exist yet.** No service, page, or repo by
that name exists anywhere in this monorepo today. Building it means a new onboarding flow that,
per signup, needs to trigger: (a) provisioning a new per-tenant IDUNA instance (see above), (b) a
new broker route so the tenant's chosen subdomain reaches it, (c) DNS + TLS for that subdomain.
None of (a)/(b)/(c) are wired together yet — this document names the shape, not a working
pipeline.

**Real, shipped this session (S243-06): the routing half of (b).** The FatBaby broker
(`PRRJECT_FATBABY/broker`, the real, already-running reverse proxy behind okemily.com's own
nginx — the "fatbaby proxies" the founder's own message referred to) previously only matched
requests by tenant bearer token or URL path prefix; it had **no Host-header (subdomain) matching
at all**, a real, decisive gap for "custom subdomains for partners/customers." Closed directly:
new `Route.Host` field (exact, case-insensitive match) + `Registry.ResolveByHost`, checked FIRST
in `AuthMiddleware` (before path-prefix and bearer-token matching, so a tenant's own paths can
never be shadowed by an unrelated global route). A Host route deliberately skips the broker's
own HTTP Basic Auth — for a tenant subdomain, the real auth boundary is that tenant's own
upstream IDUNA instance (JWT/OAuth), not the broker. 4 new tests, `go build/vet/test ./...`
clean, zero regressions (`PRRJECT_FATBABY` commit `5876dce`). **What this does NOT do**: actually
register a new tenant's route — that's still a manual edit to
`gpt2-alpine-c/config/broker-routes.json` today, the same real, honest gap this repo's own
`broker/registry.go` doc comment already names for hot-reload in general. A real
`console.okemily.com` signup flow would need to write a new Route entry into that file (or a
future DB-backed registry) programmatically, not by hand — real, separate, unbuilt work.

**Real, checked finding: wildcard TLS needs a DNS-01 challenge, not the existing HTTP-01 flow.**
`okemily.com`'s own DNS is confirmed live on Cloudflare (`nicolas.ns.cloudflare.com` /
`jocelyn.ns.cloudflare.com`). Every existing cert on this box (`certbot --nginx -d ...`, see
`sudo-queue/13-carepyre-domain-setup.sh` for the exact real pattern used elsewhere in this
monorepo) uses HTTP-01, which cannot issue a wildcard cert for `*.console.okemily.com` — only a
DNS-01 challenge can, which for Cloudflare means the real `certbot-dns-cloudflare` plugin plus a
real Cloudflare API token (a credential this investigation did not have access to and did not
attempt to source). Two real, honest alternatives, not chosen between here: (1) a real wildcard
cert via DNS-01, issued once, reused for every subdomain — the standard approach for this shape
of product; (2) issue a real, individual cert per new tenant subdomain on signup (more moving
parts per onboarding, no wildcard-cert/API-token dependency). This is a real, concrete decision
point for whoever builds the actual onboarding automation, not resolved in this pass.

**Bottom line**: the founder's proposed mechanism is sound and mostly already low-cost given
what IDUNA and the broker already are — DB-per-install needs an orchestration layer (not a
database redesign), and subdomain routing's own capability gap is now closed. What's still fully
unbuilt: `console.okemily.com` itself (the actual onboarding UI/flow), the automation that wires
a new signup into a new IDUNA instance + broker route + DNS/cert, and the wildcard-vs-per-tenant
TLS decision above.

## IDUNA_PRO — a real extraction plan (2026-09-03, founder real-time — resolves Open Question 1)

Founder, real-time, preserved verbatim, in two messages: "so we need multi tenant iduna as a
platform we can offer a free trial that really stands up their iduna instance — we pull some of
the more custom stuff out of iduna and the code goes right into the emily for business product
IDUNA_PRO" then "so we use our IDUNA to manage the free trials for emily for business." **This
resolves Open Question 1 above, decisively**: this repo (internal IDUNA) is NOT being
externalized in place. It stays exactly what it already is — the internal EINHORN_INDUSTRIAL
backbone — and additionally becomes the **control plane** for a new, separate, sibling product,
`IDUNA_PRO`, built from a real subset of this codebase. Checked, not assumed: no `IDUNA_PRO`
repo exists on GitHub yet (attempted clone, `Repository not found`) — named here, not
pre-created, unlike `EMILY_FOR_BUSINESS`.

### The control-plane model

Internal IDUNA gains a new, real capability (not yet built, scoped here): tracking and
provisioning `IDUNA_PRO` tenants, the same way it already tracks HEIMDAL sprints or Apples — a
new `tenants`/`trials` table plus a real provisioning pipeline triggered by a signup on
`console.okemily.com`:

1. `console.okemily.com` (itself unbuilt, see above) posts a new trial signup to internal
   IDUNA's own API (org name, contact, desired subdomain).
2. Internal IDUNA provisions a real, fresh `IDUNA_PRO` instance for that tenant: a new
   `SQLITE_PATH`, a fresh migration run (already proven trivial, see above), a seeded
   `agents.json`, on its own port.
3. Internal IDUNA registers a new broker `Route` (`Host: "<tenant>.console.okemily.com"`,
   `UpstreamBase` pointing at the new instance's port) — the real routing capability S243-06
   already shipped this session, just not yet wired to an automated writer.
4. Internal IDUNA tracks the tenant's real lifecycle (trial/active/expired) — a genuinely new
   subsystem, not a re-purposing of anything that exists today.

This is coherent with IDUNA's own stated identity ("IDUNA is not a product, it is the backbone")
in a way pure externalization wasn't: it stays the backbone, and the backbone now also backs the
business side, literally.

### What "pull the more custom stuff out" means — a real, checked categorization

`IDUNA_PRO`'s own codebase is a new, separate extraction of the generic parts of this repo — not
a fork of the whole thing, not a build-tag/feature-flag split of this same binary. Checked
directly against this repo's actual `internal/` and `internal/http/handlers/` directories
(2026-09-03):

**Real core candidates — generic IAM/zero-trust primitives, no EINHORN_INDUSTRIAL-specific
content:**
- `internal/auth/*` — Google OAuth, ES256 JWT issuance, M2M agent auth, device flow. The actual
  product.
- `internal/store/*` — SQLite + MySQL backends, the migrations engine. Already tenant-shaped by
  accident (see "DB-per-install" above).
- `internal/userlog/*` — the unified Splunk-shaped logging backend (SECTION 226) — this
  document's own earlier "what's real today" section already named this as genuinely sellable
  on its own.
- `internal/util/*` — generic helpers (rate limiter, etc).
- `http/handlers`: `auth.go`, `agents.go`, `jwks.go`, `device.go`, `local_auth.go`, `refresh.go`,
  `register.go`, `me.go`, `admin.go`/`admin_login.go` (core RBAC/admin), `apples.go` (the
  append-only audit-ledger MECHANISM is generic and real product value, even though "Apples" as
  a name/concept is EINHORN-flavored — would need a generic rename for the product), `logs.go`/
  `portal.go` (the log search UI).

**Real "leave behind" candidates — genuinely monorepo-specific, no product value outside this
org:**
- `internal/blog`, `internal/tyler`, `internal/promptoverse`, `internal/mailinglist`,
  `internal/drive` (Google Drive service-account integration for training artifacts),
  `internal/vault` (explicitly "founder-only password manager" per its own doc comment),
  `internal/statuspage` (checks specifically EINHORN_INDUSTRIAL's own public services),
  `internal/backlog` (the kanban-over-`EMILY/BACKLOG.md` bridge — tied to this specific file).
- `http/handlers`: `blog.go`, `tyler.go`, `mailinglist.go`, `carepyre_contact.go`,
  `promptoverse*.go`, `mmo_*.go`, `redgarden_*.go`, `shankpit_*.go`, `racer_ticket.go`,
  `papercraft_ticket.go`, `players*.go`, `player_*.go`, `chat_messages.go`,
  `admin_dragonsnshit.go`, `admin_gm.go`, `admin_saga.go`, `web_ceremony.go`, `monitors.go`,
  `intelligence.go`, `status_page.go`, `kanban_*.go`, `kgraph.go`, `dis.go`, `supply.go`,
  `heimdal.go` (MJOLNIR-sprint-specific), `push_tokens.go` (MJOLNIR FCM-specific).

**Real, genuinely ambiguous — a decision, not a fact, for whoever does the actual extraction:**
- `internal/honorcode` — the MECHANISM (a versioned, hash-verified, re-acceptance-on-bump
  covenant) is generic and could be a real product feature (compliance acknowledgment flows);
  the actual CONTENT (`THE_HONOR_CODE` text) is EINHORN-specific and would need to become
  pluggable/configurable, not shipped as-is.
- `subscriptions.go` (`Emily+` subscription provisioning) — billing-adjacent; could plausibly
  inform `IDUNA_PRO`'s own real tiering/plan model rather than being discarded outright, but
  today's implementation is almost certainly EINHORN-specific in its details.
- `kanban.go`/`kanban_page.go`/`kanban_inbox.go` — a genuinely well-built, real feature (drag/
  sort/inbox-sync, this same session's own S207-68/S235-01 work), but tightly coupled to
  parsing `EMILY/BACKLOG.md`'s own specific Markdown convention — real, substantial
  generalization work needed before this could be a customer-facing feature, not a copy-paste.

### Real, honest, not attempted in this pass

This is a categorization and a control-plane design, not a migration. Not done here: creating
the `IDUNA_PRO` repo, actually moving code, building the `tenants`/`trials` table or its
provisioning pipeline, or `console.okemily.com` itself. Real, concrete next step: found the
`IDUNA_PRO` repo (or ask the founder to pre-create it, matching the `EMILY_FOR_BUSINESS`/`LO`/
`MIXFORGE` precedent of the founder creating the empty upstream repo ahead of the work), seed it
with the "real core candidates" list above, and treat the "ambiguous" list as real, individual
decisions to make one at a time during that extraction — not resolved wholesale here.
