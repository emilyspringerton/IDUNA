# IDUNA (VS0)

IDUNA is the **social network of tournaments** component for VS0.

## Activated Mode

**Mode entered:** `IDUNA / VS0 social tournament network` with:
- Google OAuth registration
- Mandatory `THE_HONOR_CODE` acceptance (versioned)
- Tournament social graph and activity feed primitives

## Product Goals

- One durable identity per person via Google (`provider=google`, `sub` as canonical external key).
- No fully active account until the user accepts the current `THE_HONOR_CODE`.
- Social tournament baseline: profiles, follows, tournaments, entrants, bracket/matches, results, feed activity.
- Security-first defaults: rate limiting, minimal PII, auditable actions.

## Core UX Flows

### A) First-time signup
1. User starts Google OAuth.
2. Backend verifies callback and resolves/creates account.
3. Backend returns session/token and `honor_code.required=true` payload.
4. Client shows honor code gate.
5. User accepts current honor-code hash.
6. Account transitions to `active`.

### B) Returning login
- If the current honor-code version/hash changed, user logs in as restricted and must re-accept before write actions.

## Data Model (Phase 1)

- `users` (`status`: `pending_honor | active | suspended`, handle/display profile fields)
- `user_identities` (provider + provider_subject unique)
- `sessions` (or token-based equivalent)
- `honor_code_versions` (versioned canonical text + hash)
- `honor_code_acceptances` (user acceptance records)
- `follows`
- `tournaments`
- `tournament_entrants`
- `matches` (+ optional `match_games`)
- `activities`
- `audit_log`

## API Surface (Phase 1)

### Auth / Honor
- `GET /auth/google/start`
- `POST /auth/google/callback`
- `POST /honor-code/accept`
- `GET /me`
- `PATCH /me`

### Social
- `POST /users/:id/follow`
- `DELETE /users/:id/follow`
- `GET /users/:id`
- `GET /feed`

### Tournaments
- `POST /tournaments`
- `GET /tournaments`
- `GET /tournaments/:slug`
- `POST /tournaments/:id/register`
- `POST /tournaments/:id/check-in`
- `POST /tournaments/:id/lock`
- `POST /tournaments/:id/start`
- `GET /tournaments/:id/bracket`
- `POST /matches/:id/report`
- `POST /matches/:id/verify`
- `POST /tournaments/:id/complete`

## Enforcement Rule

For all write endpoints, if user is not `active` or has not accepted the current honor code:
- return `403 HONOR_CODE_REQUIRED`
- include the current honor-code payload so clients can route the user to acceptance.

## Current Web Routes

- `/` — VS0 registration (Google OAuth -> THE_HONOR_CODE -> handle)
- `/event-stream/` — read-only event viewer
