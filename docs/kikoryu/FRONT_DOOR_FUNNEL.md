# FRONT_DOOR_FUNNEL — Agents and Unagents at IDUNA's One Front Door

`supersedes: none` — new synthesis. Does not supersede `VS0_IDENTITY_GATE.md` or
`VS7_AGENT_AUTHORITY.md`; it sits between them, answers the question neither
was written to answer (should the two onboarding paths be symmetric?), and
cites both as living documents. If either is revised later, revise it there;
this document points at them rather than restating their content.

`status: draft — code-verified, proposing new build steps`
`role in tournaments platform: this is the gate every entrant, human or
machine, passes through before VS2/VS7/VS9/VS10 have anything to say about them`

*2026-07-23. Author: Claude Code (Fable), at founder direction, following the
2026-07-16 VS reality audit. See `docs/VS_REALITY_AUDIT.md` §VS0/§VS7 and
`docs/kikoryu/VS0_IDENTITY_GATE.md` / `VS7_AGENT_AUTHORITY.md` for the
reconciliation this document builds on.*

---

## 1. The asymmetry, stated precisely

IDUNA has two live entry paths and they are not different implementations of
one idea — they are two different *shapes* of process:

| | Human / Unagent | Agent |
|---|---|---|
| Entry mechanism | Self-service: Google OAuth → device bridge → honor-code acceptance → gamertag claim | Human-operated: hand-edit `config/agents.json` **or** fill the `/admin/agents` form, run `cmd/bootstrap` |
| Who acts | The arriving identity itself | A third party, on the identity's behalf, before it "arrives" at all |
| State visible anywhere? | Implicitly, via exchange-time errors (`ErrHonorCodeRequired`, `ErrHandleRequired`) — see VS0 rewrite §"known divergences" | No. An agent is either a JSON object plus a DB row, or it doesn't exist. There is no state between "not created" and "fully capable" that the system tracks. |
| Consent moment | Yes — THE_HONOR_CODE, deliberately built as ritual (rose gold, "We Agree", `docs/archive/kikoryu-vs-original/vs0.md` §5) | None. Nothing analogous exists, and (this document's position, §2) nothing analogous *should*. |
| Audit trail of "why does this identity exist" | Implicit in Google account + honor-code acceptance timestamp | `AgentCreated` event exists in `iam_event_stream` (`internal/store/mysql.go` `CreateAgent`) **only** when created via `/admin/agents`; the `cmd/bootstrap` + `config/agents.json` path never appends an event — an agent seeded that way has a row and a JSON diff in git history, and nothing else. |

**This asymmetry was never decided. It grew.** The rest of this document
decides it, on both axes the prompt separates: onboarding (how do you arrive)
and governance (what can you do once here, and can we prove it and pull it
back). VS7's four principles — least authority, full auditability, instant
revocability, visibility boundaries — are governance. This document is almost
entirely about the axis before that: arrival.

## 2. The core call: agents get a lifecycle, not a ceremony

**Position:** agent onboarding should gain a real, tracked, auditable
*lifecycle* — but explicitly **not** a ceremony, and not a consent moment.
These are different things and the difference is load-bearing.

**Why not a ceremony.** THE_HONOR_CODE works because the party accepting it
has standing to agree to something: a human, capable of consent, bound by the
agreement afterward in a way that matters morally and legally. An agent — a
`GoogleAuthHandler`-adjacent claude process, a cron daemon, `karen`, `saga` —
has no such standing. Building a screen where an agent's provisioning script
clicks "We Agree" on the agent's behalf would be theater: it would look like
the agent consented to its capability scope, when what actually happened is a
human decided the scope and the agent complied because software complies.
Pretending otherwise blurs exactly the line VS7 draws between **Agents**
(bounded-authority actors) and moral participants — and the audit's own
finding is that this line is IDUNA's one genuinely-shipped, load-bearing
distinction (`VS7_AGENT_AUTHORITY.md` "not a false cognate"). A ceremony for
agents would spend that distinction to buy nothing.

**Why not "stay static," either.** The founder's instinct that "agents aren't
moral actors so maybe the status quo is correct" gets the actor part right and
the process part wrong. The thing missing today isn't ritual — it's
*structure*: a way to know, for any agent, which of "declared to exist,"
"has an accountable owner," "has a reviewed capability scope," and "is
actually usable" is currently true. Right now those four facts collapse into
one boolean (`agents.status = ACTIVE`) the instant a row is inserted — see
§4.1 for the concrete, verified gap this produces. That's not "agents
correctly get no ceremony." That's dark matter: state the system has but does
not track, exactly DOC-102's failure mode 3.

**The resolution:** replace *consent* with **declaration**, and make it a
state machine, not a click. The accountable party is not the agent — it is
the agent's `owner_user_id`, a real IDUNA Unagent who has *already* passed the
VS0 gate (see §5, the one deep structural link between the two funnels: no
agent can be onboarded whose owner hasn't already been onboarded). The
"equivalent of the Honor Code" for an agent is not the agent agreeing to
anything. It is the owner's act of declaring, on the record: *this identity
exists, I am accountable for it, here is exactly what it may do.* That
declaration is auditable, revocable, and — this is the point — currently only
half-built.

### 2.1 The agent lifecycle

```
PROPOSED ──► CUSTODIED ──► SCOPED ──► LIVE
                                        │
                            ┌───────────┼───────────┐
                            ▼                       ▼
                       SUSPENDED             DECOMMISSIONED
```

| State | Meaning | Verified today? |
|---|---|---|
| `PROPOSED` | An agent identity is declared to exist: name, type, purpose. No DB row required yet — this can be a PR against `config/agents.json`, or an unsaved `/admin/agents` form. | N/A — pre-system state, same as ANON |
| `CUSTODIED` | A DB row exists (`agents` table) with a **valid** `owner_user_id` — enforced today by `fk_agents_owner REFERENCES users(id)` (`202602220002_iam_rbac.sql:58`). This is the human-accountability declaration; it is the direct analog of HONOR, redirected from the agent to its owner. | **Live** — the FK constraint already makes this true structurally; what's missing is that it isn't *surfaced* as a lifecycle stage anywhere (§4.1) |
| `SCOPED` | `agent_permissions` rows exist — the capability manifest is populated and reviewed. Direct analog of HANDLE: a name is not enough, the *scope* has to be locked in before the identity can act. | **Half-live** — `agent_permissions` exists and is enforced at auth time; there is no admin-console path to populate it (§4.1), only `cmd/bootstrap` reading `config/agents.json` |
| `LIVE` | `api_key_hash` is set (`SetAgentCredential`) and the agent has completed at least one successful `POST /api/v1/auth/agent` exchange. Direct analog of READY: all gates cleared, handoff to the agent's own runtime is live. | **Half-live** — `SetAgentCredential` exists in the store layer and even emits `AgentCredentialSet` to `iam_event_stream`, but **no HTTP handler calls it** (verified: only test stubs reference it, `internal/http/handlers/agent_auth_test.go`). Only `cmd/bootstrap`'s `provisionSecrets` reaches it. |
| `SUSPENDED` / `DECOMMISSIONED` | `agents.status` already models this (`ACTIVE|SUSPENDED|DECOMMISSIONED`); `/admin/agents` suspend/activate actions are live and audited (`UpdateAgentStatus` → `iam_event_stream`). | **Live** |

Note what this lifecycle is *not*: it has no "acceptance" step for the agent
itself, no UI the agent interacts with, and no rose gold anywhere in it. It
is entirely a record of what an accountable human has declared, in the same
register as a change ticket, not an oath. Where VS0's states are gated by the
*participant's own actions* (accept the code, claim the tag), every one of
these states is gated by an *operator's* action taken on the agent's behalf.
That's the asymmetry, kept, and now it's a decision instead of an accident.

## 3. What's shared, what's deliberately separate

**Genuinely shared** (the trust substrate, not the funnel):

- **JWT issuance core** — `internal/auth/jwt.Sign`/`Keys`, one ES256 keypair,
  one `/.well-known/jwks.json`. Already shared; stays shared. No reason for a
  second signing identity — downstream services already trust "an IDUNA JWT,"
  not "a human IDUNA JWT" vs. "an agent IDUNA JWT."
- **The audit ledger** — `iam_event_stream` (+ the Apples ledger downstream of
  it). Every onboarding-lifecycle transition, human or agent, is an event in
  the same stream, queryable the same way. SAGA and KAREN, once they exist,
  read this stream uniformly; it should never fork into "human events" and
  "agent events."
- **The RBAC grammar** — `domain.action` permission strings, enforced by the
  same `internal/http/middleware` regardless of whether the bearer claim came
  from a `roles[]`-shaped Unagent JWT or a flat `permissions[]`-shaped agent
  JWT. Two tables (`role_permissions` vs `agent_permissions`), one grammar,
  one enforcement point.
- **The Back Office** (`/admin`) as the *one* pane of glass. It already
  half-does this — `/admin/users` and `/admin/agents` are siblings in the same
  nav bar today. §4 closes the gap that makes `/admin/agents` currently
  produce agents that can't do anything.
- **The owner-chain constraint** (§5): every agent traces to an Unagent who
  already cleared VS0. This is the one place the two funnels are structurally
  entangled, and it should stay that way — it's the actual accountability
  chain, not an implementation accident.

**Deliberately separate**, and should stay that way:

- **Credential exchange protocol.** Google OAuth + device-code bridge for
  humans; `agent_name`+`agent_secret` M2M exchange for agents
  (`POST /api/v1/auth/agent`). Different trust models — one authenticates a
  person via a third-party IdP across a UI a browser renders, the other
  authenticates a already-provisioned static secret a human already knows.
  Merging these into one endpoint would weaken both: the human path needs
  OAuth's phishing resistance and rate-limited polling; the agent path needs
  none of that and gains nothing from carrying it.
- **The ceremony UI.** `index.html`/`app.js`/`styles.css` (rose gold, Cormorant
  Garamond, "We Agree") is a Unagent-only artifact. Agents never render a
  page; `/admin/agents` is an *operator* console, not a participant-facing
  ceremony, and should keep looking like the Back Office's clerical register
  aesthetic (`iam-spec.md` §1.2/§5), not VS0's front-office one.
- **Threat model and rate limiting.** The human funnel faces the open
  internet pre-authentication and needs `WindowRateLimiter`s, DIS posture
  awareness, and (per VS0) is a plausible target for credential-stuffing and
  abuse at scale. The agent funnel is always initiated by an already-trusted
  operator in an admin session or a shell with repo access — the risk there is
  credential *leakage/rotation*, not anonymous signup abuse, and the controls
  should match (secret rotation discipline, not CAPTCHAs).
- **Self-service.** Humans drive their own state transitions. Agents never
  do — even `LIVE`'s "first successful exchange" is triggered by the agent's
  *runtime* starting up with a secret a human already handed it, not by the
  agent making a choice. No transition in the agent lifecycle is ever
  initiated by the agent itself. This is intentional, not a UX gap to close
  later.

**Is there a shared "front door" concept at all?** Not as one funnel with two
branches — that would force one of the two shapes to distort itself to match
the other, which is exactly the symmetry-for-its-own-sake this document
argues against. The honest description: **two separate funnels, one shared
trust substrate.** More than "share a signing key" (the prompt's low bar) —
they share the ledger, the RBAC grammar, and the console — but they remain
two funnels because arrival means something different for each kind of
actor, and a design that pretends otherwise either infantilizes the human
ceremony or dresses up the agent bootstrap in borrowed solemnity.

## 4. Onboarding vs. governance — where one ends and the other begins

The prompt is right that these are adjacent, not identical, and asks that the
line not be collapsed. Here it is, precisely:

- **Onboarding (this document) answers:** does this identity exist yet, who
  is accountable for it, what has been declared about its intended scope, and
  is it currently capable of authenticating at all. Its unit of concern is
  the **identity record** — one row in `users` or `agents`, plus the events
  that constructed it. It is a *finite* process with a terminal state
  (`READY`/`LIVE`) after which onboarding has nothing further to say.
- **Governance (VS7, `VS7_AGENT_AUTHORITY.md`) answers:** given that this
  identity exists and is live, what may it do *right now*, is every privileged
  action it takes audited, can its authority be pulled back instantly, and
  does it see only what its role requires. Its unit of concern is the
  **action** — every `AuthorityActionExecuted`-class event, forever, for as
  long as the identity is `LIVE`. It has no terminal state; it runs for the
  entire lifetime of an `ACTIVE` agent or user.

Concretely: `agent_permissions` rows are *populated* during onboarding
(`SCOPED`) but *enforced* during governance (every request, by middleware,
against the store's current truth, never the JWT's stale claim — carried
forward from VS7 unchanged, `iam-spec.md` §6). Onboarding writes the
capability manifest once (and on later amendment); governance reads it on
every single privileged call. A design that fused these would either force
every API call to re-run the onboarding declaration (absurd) or let
onboarding silently double as a runtime authority check (which is precisely
how "state modeled as an error at exchange time" happened to VS0 — the gate
and the ongoing check merged into one code path, and now nobody can query
"what state is this user in" without inference). Keep them separate
mechanically, even though one hands off into the other.

### 4.1 The concrete gap this section exists to name

Verified live, 2026-07-23: `/admin/agents`'s "Register New Agent" form calls
`store.CreateAgent`, which inserts the row with **`status = 'ACTIVE'`
unconditionally** (`internal/store/mysql.go:227-252`,
`internal/store/sqlite.go:298`ff) — i.e., today's Back Office console jumps an
agent straight from nothing to nominally `LIVE`-labeled with **zero**
`agent_permissions` and **zero** credential, because no admin handler calls
`SetAgentCredential` or grants any permission. Such an agent cannot
authenticate (`AuthenticateAgent` requires a non-empty `api_key_hash`) and
cannot do anything even if it could (no rows in `agent_permissions`). It is
inert — a row that looks live and isn't. This is the asymmetry's sharpest live
symptom: the *only* path that currently produces a genuinely working agent is
`config/agents.json` + `cmd/bootstrap`, a path that never touches the Back
Office at all and never appends an `AgentCreated` event. §6 fixes this first,
before anything else, because it's the cheapest and most concrete step
available and it's currently misleading whoever uses that console.

## 5. The one real coupling between the two funnels

Every agent's `owner_user_id` is a foreign key into `users(id)` — not
optional, enforced at the schema level today. That means, structurally,
**an agent can only be `CUSTODIED` if a human has already reached `READY` in
the Unagent funnel.** KAREN and SAGA, when they register, will each need a
real `users` row behind their `owner_user_id` — presumably the founder's own
Google-OAuth'd, honor-code-accepted identity, or a designated ops account,
already `READY` per VS0. This is worth stating explicitly because it means
the Unagent funnel is not just "the other funnel" — it is a **prerequisite**
the agent funnel already silently depends on. No change needed; this is
already true and already enforced. It just wasn't written down anywhere,
which is exactly the kind of dark matter this document exists to close.

## 6. VS2 (tournaments) and the funnel — position taken

**Tournament-specific gating (age/jurisdiction attestation, play-money-only
acknowledgment) belongs downstream of the front-door funnel, as VS2's own
concern — not as a fifth VS0 state.** Argued:

1. **VS0 is a generic identity gate.** Every consumer of IDUNA identity today
   — SHANKPIT, DragonsNShit/GoblinFoxDragon, MJOLNIR, and eventually the
   tournaments platform — authenticates through the *same* `READY` state and
   the *same* device-auth bridge (`VS0_IDENTITY_GATE.md` "Device auth as the
   bridge for any non-browser client. Tournament table clients authenticate
   exactly as the MMO client was going to"). Embedding a tournaments-specific
   attestation into that gate would force every other consumer — a SHANKPIT
   player who will never see a poker table — to either carry irrelevant state
   or trigger special-cased skip logic. That's the wrong direction for a gate
   whose entire value is being the *one* thing every consumer can rely on
   looking the same.
2. **VS2's own rewrite already treats registration as its own consent
   moment**, correctly: *"REGISTERING: entrant list + start timer public;
   registration is the rose-gold consent moment"* (`VS2_TOURNAMENTS.md`
   §Lifecycle). That's not accidental convergence with this document's
   argument — it's the same principle applied a layer down: **every product
   that needs its own irreversible agreement gets its own rose-gold moment,
   reusing VS0's established visual and procedural grammar (a second
   `honor_code`-shaped versioned acceptance, scoped to `tournaments`, not
   bolted onto the identity gate's own `honor_code_version`).** Age and
   jurisdiction attestation, and the play-money/non-redeemable acknowledgment
   VS2 hard-requires, are exactly this: a second, tournament-scoped honor
   code, accepted at first tournament registration (or account-level, once,
   before first entry), stored the same shape as `honor_code_version`/`_sha`
   but namespaced (e.g. `tournament_terms_version` on a `users` or
   `tournament_entrants` row) rather than overloading VS0's field.
3. **This keeps VS0 stable while VS2 is still unbuilt.** VS2 is, per the
   audit, "not-yet-built-still-relevant — THE PRIMARY DIRECTION" — its exact
   attestation requirements (which jurisdictions, what age floor, what
   specific play-money language) are product decisions VS2's own build order
   should make, not decisions this document should freeze into VS0's schema
   pre-emptively. If VS2 needs a new state, it should be a VS2 state
   (`tournaments.entrant_status` or similar), consumed by the tournament
   registration handler, not a new branch in `/auth/device`'s exchange logic.

**Where it doesn't belong:** inside `ErrHonorCodeRequired`/`ErrHandleRequired`
gating at `/auth/token/exchange`. That code path is genuinely generic today
and should stay that way.

## 7. Migration path — incremental, no flag day

Ordered by dependency; each step is independently shippable against the live
system with real registered agents (`EMILY-PRIME`, `FATBABY-EMILY`, `EMIREE`,
`JON`, `BOB`, `TYLER`, `EDIS-WOOCOMMERCE`, `EMILY-TRAINING`, …) continuing to
authenticate unchanged throughout.

1. **Close the `/admin/agents` gap (§4.1).** Add `GrantAgentPermission` /
   `RevokeAgentPermission` store methods (mirroring the existing user-role
   grant pattern at `admin.go:460-480`) and a `POST /admin/agents/{id}/secret`
   action that calls the already-existing, already-tested `SetAgentCredential`
   and displays the plaintext exactly once (same one-time-reveal pattern
   `cmd/bootstrap` already uses for `var/agent-secrets.env`). Change
   `CreateAgent` to insert `status = 'PENDING'` (new enum value, additive
   migration) instead of `'ACTIVE'`, flip to `'ACTIVE'` only once both a
   credential and at least one permission exist. Zero risk to the live
   bootstrap path — `cmd/bootstrap` never calls `CreateAgent`, it inserts
   directly via `config/agents.json` seeding and would need its own explicit
   opt-in to the new enum value (default it to `'ACTIVE'` there, preserving
   exact current behavior for every already-registered agent).
2. **Surface the lifecycle, don't gate on it yet.** Additive migration:
   `agents.onboarded_via` (`'bootstrap_json' | 'admin_console'`),
   `agents.stage` (`'CUSTODIED' | 'SCOPED' | 'LIVE'`, computed from existing
   facts — owner FK present, `agent_permissions` non-empty, `api_key_hash`
   non-empty — backfillable in one UPDATE for all existing agents, all of
   which land at `LIVE`/`'bootstrap_json'`). No enforcement changes anywhere;
   this step only makes visible what's already true.
3. **Wire `AgentCreated`/`AgentCredentialSet`/permission-grant events into the
   Back Office Agent Registry view** as a per-agent timeline (mirrors the
   existing `/admin/audit` viewer, filtered to one aggregate) — the "Agent
   Registry" console named in the original VS7 spec's Backend Tooling section
   and never fully built, finally matching its name.
4. **Onboard KAREN and SAGA through the closed-loop `/admin/agents` path**
   (step 1), not `config/agents.json` — the near-term test case the prompt
   asks for. This validates the whole lifecycle against a real build instead
   of a hypothetical, and gives both agents a real `AgentCreated` event and a
   real onboarding timeline from day one, unlike every agent before them.
5. **Fix the VS0 web ceremony's stale bindings** (`app.js` calling
   `/auth/google/start`, `/me`, `/me/handle` — none registered; audit's own
   follow-up item) — required regardless of this document, but load-bearing
   for step 6 since the ceremony has to actually work end-to-end once it's
   the only thing served at the new origin.
6. **Resolve the root-path collision (nginx).** Apply the four already-drafted
   location blocks in `ops/nginx-front-door-snippet.conf` (`/api/v1/`,
   `/.well-known/jwks.json`, `/auth/`, `/admin/`) to `iduna.farthq.com`
   immediately — they don't touch `location /` and are safe today.
   For the ceremony frontend itself (`GET /{$}` at `main.go:323`), **give it a
   dedicated subdomain** — e.g. `gate.farthq.com` — proxied straight to
   `127.0.0.1:8080`, rather than fighting WordPress's catch-all for `/` on
   the shared domain. Argued briefly, as asked: a subdomain (a) needs no
   location-block ordering games against a catch-all `try_files` block that
   could regress the moment someone edits the WordPress vhost for an
   unrelated reason; (b) gives the ceremony its own cookie/session scope,
   which matters for a page whose entire job is a security-sensitive
   handoff; (c) keeps certbot's pending `iduna.farthq.com` cert issuance
   (blocked on the S23-01b domain-merge decision) completely decoupled from
   shipping the funnel — the funnel doesn't have to wait on that decision at
   all if it lives elsewhere. This is the one open item this document
   resolves that `ops/nginx-front-door-snippet.conf`'s own comment block
   explicitly deferred to "Fable's call." One sentence on EDIS: once
   `SECTION 35` (EDIS DIS production hardening) matures enough that
   WordPress is trusted with identity-adjacent surfaces, this decision is
   revisitable — not before, and not as part of this document's scope.
7. **Backfill the agent lifecycle onto pre-existing agents in the audit
   trail** — a single retroactive `AgentCreated`-equivalent event per agent
   citing `onboarded_via: bootstrap_json` and a `migration-note` payload, so
   `iam_event_stream` reads as complete history rather than having eight
   agents with no origin event and two (KAREN, SAGA) that do.

Steps 1–4 require no nginx/DNS change and can land immediately. Steps 5–6
are the literal unblock for `EMILY/BACKLOG.md` S23-01b and should be
sequenced together. Step 7 is cleanup and can land whenever.

## 8. Explicit non-scope

- Tournament attestation schema itself (age/jurisdiction fields, exact
  wording) — VS2's build order, not this document's.
- Session revocation (VS1's open gap) — a prerequisite for governance-side
  instant revocability, not for onboarding; referenced, not re-specified.
- EDIS/WordPress hardening — `SECTION 35`, out of scope here by design.
- The moderator/tournament-director *authority* role itself — VS7's
  rewrite already specs `TOURNAMENT_DIRECTOR`; this document only establishes
  that such a role's *human* holder still onboards via the ordinary Unagent
  funnel (a director is a Unagent who was later granted authority, not a
  third kind of funnel).
