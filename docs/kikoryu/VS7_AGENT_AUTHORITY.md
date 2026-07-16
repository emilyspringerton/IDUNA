# VS7 — Agent Authority: Reconciling "Agents vs. Unagents" with IDUNA's Live Agent Model

`supersedes: vs7.md` (archived at `docs/archive/kikoryu-vs-original/vs7.md`)
`status: reincarnated-elsewhere — genuine architectural cousin, reconciled here`
`role in tournaments platform: director/moderator authority model`

*2026-07-16. See `docs/VS_REALITY_AUDIT.md` §VS7.*

---

## The finding: not a false cognate

VS7 proposed a two-class world: **Unagents** (ordinary participants bound by
world rules) and **Agents** (identities carrying bounded, audited, revocable
authority — GM_AGENT, MOD_AGENT, SYS_AGENT). IDUNA independently built this
model and runs on it today — grown from the SYS_AGENT corner outward:

| VS7 principle | Live implementation (verified) |
|---|---|
| Agents as first-class non-user identities | `agents` table, distinct from `users`, with accountable human owner (`agents.owner_user_id`) — `migrations/truestore/202602220002_iam_rbac.sql` |
| Least authority | `agent_permissions`: **explicit direct capabilities, no role inheritance** (CLAUDE.md: "JWT with explicit permissions[] (no role inheritance)") |
| Bounded credentialing | M2M exchange `POST /api/v1/auth/agent` (`internal/http/handlers/auth.go:107`, `internal/auth/agent.go`); registry seeded from `config/agents.json` |
| Instant revocability | `agents.status` ENUM `ACTIVE|SUSPENDED|DECOMMISSIONED`; suspension specified to kill token workflows (`docs/iam-spec.md` §4.2 `AgentSuspended`) |
| Full auditability | `iam_event_stream` ledger; governance events `AgentCreated`/`AgentSuspended`/`AuthorityActionExecuted` (iam-spec.md §4.2); plus the Apples ledger as the operational action log |
| Agent registry console | `/api/v1/agents` + Back Office agent views (`internal/http/handlers/agents.go`, `admin.go`) |

Every production actor — EMILY_PRIME, EDIS-CUSTODIAN, tyler-agent
(`202606070001_tyler_agent.sql`), bob-agent, and SAGA when it registers
(HQ-SPEC-DOC-102: "registered in Iduna as `saga`") — is a VS7 SYS_AGENT in
everything but vocabulary. The concept shipped; the paper trail didn't.

## Vocabulary reconciliation (canonical from now on)

- VS7 "Agent" = IDUNA **agent** (M2M identity) *or* a human user holding
  authority capabilities. Authority is a property of capabilities, not of
  being a machine.
- VS7 "Unagent" = ordinary user: gate-cleared (VS0), no authority
  capabilities. No new term needed.
- VS7 "SYS_AGENT" = today's registered M2M agents. **Fully live.**
- VS7 "GM_AGENT"/"MOD_AGENT" = **not built** — these are *human* authority
  classes, and they are what the tournaments platform needs next.

## What to build: tournament-director authority (the GM/MOD gap)

The tournaments platform (VS2) needs human authority with VS7's exact
properties:

- **TOURNAMENT_DIRECTOR** (MOD_AGENT/GM_AGENT hybrid): capability set on the
  order of `tournaments.moderate`, `tournaments.intervene`, `users.suspend`,
  `scoreboards.publish` (VS10) — and pointedly *not* `iduna.admin`, not
  billing, not schema. Granted via existing `roles`/`role_permissions`
  machinery; no new mechanism required, only seeds and enforcement points.
- **Authority actions as events**: every intervention (seat correction,
  disqualification, standing correction) emits `AuthorityActionExecuted`-class
  events with actor, payload, justification — the machinery iam-spec.md
  already specifies.
- **Revocation that bites**: depends on VS1's session-revocation gap
  (`docs/kikoryu/VS1_IAM_MODERATION.md` §gap 1). Emergency de-authorization
  is a rose-gold Back Office action.

## Carried forward unchanged

The four governance principles — least authority, full auditability, instant
revocability, visibility boundaries — are adopted as stated in the original.
The first three are live for M2M agents; visibility boundaries (an authority
sees hidden state only if the role requires it) become relevant with
tournament directors (e.g. a director must never see hole cards of live hands
except in dispute review of *completed* hands).

## Explicitly retired

The MMO-specific console surfaces (teleport players, spawn entities, world
resync) — those belong to whatever world engine hosts them (DragonsNShit has
its own world-event admin in `internal/http/handlers/mmo.go`). IDUNA's VS7
scope is identity, capability, audit, and revocation — the trust substrate.
