# VS0 — The Identity Gate

`supersedes: vs0.md` (archived at `docs/archive/kikoryu-vs-original/vs0.md`)
`status: live-but-undocumented → documented by this rewrite`
`role in tournaments platform: foundation — every entrant passes this gate`

*2026-07-16. Code-verified rewrite; see `docs/VS_REALITY_AUDIT.md` §VS0 for
the full reconciliation.*

---

**Resolved, 2026-07-16, same day:** the founder's "primary in and out is emails — VS0 is emails"
turned out not to be about this document at all — clarified as Emily Prime's own operational email
fabric (AM/PM status digests, founder-directive intake, MJOLNIR push receipts), tracked at
`EMILY/BACKLOG.md` SECTION 149. This VS0 rewrite stands unchanged; no reconciliation needed.

---

## What VS0 is now

VS0 is IDUNA's player identity gate: the path from an external Google identity
to a tournament-ready participant with an accepted honor code and a permanent,
unique gamertag — plus the device-auth bridge that lets non-browser clients
(game clients, tournament tables) authenticate against the web session. The
original spec framed this as the ceremonial entry into a single-shard MMO;
the mechanism is unchanged, but its consumer is now **the social tournaments
platform**: the gamertag is the entrant's permanent table identity (VS2), and
honor-code acceptance is the conduct baseline that moderation (VS1) and
reputation (VS9) enforce against.

## What is live today (verified)

| Spec concept | Reality | Where |
|---|---|---|
| Google OAuth → internal identity | Live | `POST /api/v1/auth/google` (`internal/http/handlers/auth.go`), `internal/auth/google/` |
| Gamertag lock-in | Live (data + claim) | `users.gamertag` VARCHAR(64) UNIQUE (`migrations/truestore/202602220002_iam_rbac.sql`); JWT `gamertag` claim (auth.go:82); echoed by `/api/v1/identities/me` |
| THE_HONOR_CODE versioned acceptance | Live (data + enforcement) | `users.honor_accepted_current/honor_code_sha/honor_code_version/honor_code_text`; `auth.User.HonorAccepted/HonorCurrentSHA/...` (`internal/auth/types.go`) |
| Device auth bridge | Live, fully wired | `/auth/device/start`, `/auth/device/poll`, `/auth/token/exchange`, `/device`, `/device/confirm` (`internal/http/handlers/device.go`, registered main.go:140); tables `device_auth_requests`, `exchange_codes`, `event_store` (`202602220001_device_auth.sql`) |
| Gating enforcement | Live, as exchange-time errors | `internal/auth/device/service.go`: `ErrHonorCodeRequired`, `ErrHandleRequired`, `ErrAccountSuspended` (service.go:197–201) |
| Rate limiting | Live | `util.WindowRateLimiter` on device start/confirm |
| Ceremonial front office | Present, served, **bindings stale** | `index.html` (honor screen, gamertag picker), `app.js` (HONOR→HANDLE→READY client states), `styles.css` (`--gold: #b89b62`, `--consent-rose: #b76e79`, Cormorant Garamond); served at `/` (main.go:210–212) |

## Known divergences from the original spec (kept honest)

1. **The ANON→HONOR→HANDLE→READY state machine is not a queryable field.**
   Gating is enforced as errors at device token exchange, and the *client*
   (`app.js`) derives the states. The rewritten contract: `/api/v1/identities/me`
   should expose `gate_state` explicitly so game clients and the tournaments
   lobby don't re-derive it.
2. **Stale web bindings.** `app.js` calls `/auth/google/start`, `/me`,
   `/me/handle`; none are registered in `main.go`. Until repaired, the web
   ceremony is unverified end-to-end. (Audit follow-up item.)
3. **Metal drift.** Spec gold `#C6A75E` shipped as `#b89b62`. Rose gold
   `#B76E79` shipped exactly and remains reserved for irreversible consent
   (honor-code agreement), per the original semantics. Treat `styles.css` as
   the living palette; the *rule* (rose gold = consent only) is what's
   canonical, not the hex.

## Carried forward unchanged

- The ceremony framing: acceptance of THE_HONOR_CODE is a versioned,
  re-acceptance-on-bump covenant, visually distinguished (rose gold) from all
  routine actions. This is now also the **conduct anchor for tournaments**:
  registration for a tournament (VS2) is a second rose-gold moment.
- Gamertag permanence: one identity, one name, forever — it is the name on
  every table, standing, and record (VS10).
- Device auth as the bridge for any non-browser client. Tournament table
  clients authenticate exactly as the MMO client was going to.

## Explicit non-scope

Email OTP/magic links and session revocation remain VS1 concerns. Tournament
mechanics are VS2. Nothing in VS0 grants authority — that is VS7.
