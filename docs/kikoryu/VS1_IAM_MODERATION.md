# VS1 — IAM, Moderation & the Back Office

`supersedes: vs1.md` (archived at `docs/archive/kikoryu-vs-original/vs1.md`)
`status: live-but-undocumented (partial) → documented; open gaps specified`
`role in tournaments platform: operational trust — moderation, audit, support`

*2026-07-16. Code-verified rewrite; see `docs/VS_REALITY_AUDIT.md` §VS1.*

---

## What VS1 is now

VS1 is IDUNA's administrative and moderation layer: RBAC, the append-only
audit record, the Back Office console, and account lifecycle control
(suspension/ban). The original spec called this "the Back Office of
immortality"; in the tournaments-platform framing it is **tournament
integrity operations** — the desk where disputes are resolved, cheaters are
suspended, and every intervention leaves a record.

## What is live today (verified)

- **RBAC**: `roles`/`permissions`/`user_roles`/`role_permissions` +
  agent-scoped `agents`/`agent_permissions`
  (`migrations/truestore/202602220002_iam_rbac.sql`), enforced by
  `internal/http/middleware`. Permission grammar is `domain.action`
  (`iduna.admin`, `monitors.read`, `subscriptions.admin`, …).
- **Event-sourced history, twice**: the `iam_event_stream` ledger (same
  migration; `RoleRevoked` etc. emitted from `internal/store/store.go`), and
  the `userlog` append-only log with the `local_users` projection
  (`internal/userlog/`, `202606180001_local_users.sql`) driving user CRUD
  (`internal/http/handlers/users.go`). The original principle — admin actions
  are events producing recomputable projections, never raw mutations — is
  genuinely implemented in the userlog path.
- **Back Office console**: `/admin` (`internal/http/handlers/admin.go`,
  `admin_login.go`), `iduna.admin` required; user listing, role assign/revoke
  (admin.go:115–121).
- **Account lifecycle**: `users.status` ENUM `ACTIVE|SUSPENDED|BANNED|PENDING`;
  suspended users are refused at device token exchange
  (`ErrAccountSuspended`, `internal/auth/device/service.go`).
- **Email auth** — shipped as **email+password**, not magic links:
  `/api/v1/auth/local` (`local_auth.go`, bcrypt) and
  `/api/v1/auth/email/register|login` (`player_email_auth.go`, SHANKPIT
  players). Auth routes rate-limited (`authRateLimit`, main.go:251).
- **JWT refresh**: `POST /api/v1/auth/refresh` (`refresh.go`) — re-issues,
  does not revoke.
- **Honor re-acceptance data model**: per-user `honor_code_version`/`sha`
  stored and compared; a forced re-acceptance *flow* on version bump is not
  verified.

## Open gaps — the parts a tournaments platform actually needs

1. **Session revocation (the biggest hole).** JWTs are stateless and nothing
   invalidates them: no denylist, no session store, `refresh.go` happily
   extends. A platform that suspends a cheating entrant mid-tournament needs
   revocation that bites within seconds. Options preserved from the original
   spec: short-TTL access tokens + revocable refresh grants, or a denylist
   checked in middleware. Either way, `UserSuspended` must imply "downstream
   token validity revoked" (iam-spec.md §4.2 already claims this; the code
   does not yet deliver it).
2. **Moderator tier.** Roles exist mechanically, but no `moderator` role or
   moderation capability set is seeded. The tournaments platform needs a
   tournament-director/moderator role distinct from `iduna.admin` — specified
   in VS7's rewrite (`VS7_AGENT_AUTHORITY.md`) as MOD_AGENT-equivalent
   capabilities (`tournaments.moderate`, `users.suspend`, no billing/schema
   access).
3. **Magic links / OTP.** Never built; email+password won instead. Keep as
   an option, not a commitment — Google OAuth + local auth covers current
   consumers.
4. **Back Office moderation surfaces.** User search by gamertag, suspension
   with recorded reason, handle arbitration, and an audit-log viewer filtered
   by actor/aggregate — the console today is thinner than the spec's desk.

## Carried forward unchanged

- Server authority: role logic is evaluated server-side; JWT claims are
  hints, the store is truth.
- Non-destructive history: every administrative action is an event first.
- The clerical aesthetic (ledger rows, compartment panels, rose gold reserved
  for irreversible acts like banishment) — see `iam-spec.md` §1.2/§5, which
  remains the living Back Office design authority.
