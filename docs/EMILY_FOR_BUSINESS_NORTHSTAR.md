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
