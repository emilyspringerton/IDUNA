# NORTHSTAR — IDUNA Vault (Password Manager)

**Status:** Draft v0.1 — northstar only, no implementation yet
**Date:** 2026-07-19
**Founder framing, verbatim:** "we need a password manager as a first class prodcut for the
founder" / "parity with password managers" / "chrome extension" / "iduna."

---

## 1. The incident that actually motivated this

Not hypothetical — it happened in this exact session, minutes before this doc was written. Setting
up Gmail OAuth required a Google Cloud Client ID and Client Secret. They ended up as two separate
plaintext files dropped directly in the home directory: `clientg_id.tct` (a typo'd filename,
correct content) and `secret.txt`. No encryption, no vault, no expiry, sitting on disk in plain
text the whole time it took to notice and use them. This is exactly the failure mode a password
manager exists to prevent, produced by the single most security-conscious operator this company
has (the founder, mid-session, moving fast) — which is the actual argument for building one, not
a hypothetical threat model.

## 2. What "parity with password managers" means, concretely

Model against the real, known feature set of 1Password/Bitwarden — the same "interface shadowing"
pattern already used for `EMILY/docs/NORTHSTAR_BABY_ERP.md` (study the real thing, build our own
implementation, zero actual dependency on the thing being studied):

- **Vault items**: logins (username/password/URL), secure notes, API keys/tokens, TOTP/2FA seeds,
  documents (small files — cert files, JSON credentials, exactly like `google-services.json` or
  the OAuth client files from §1).
- **Master passphrase, never stored.** Already have the correct primitive for this —
  `internal/mailinglist/crypto.go`'s `Vault`: Argon2id key derivation (RFC 9106 parameters),
  AES-256-GCM, key held only in server memory after explicit unlock, a canary value to detect a
  wrong passphrase, and a *deliberate* accepted cost that a process restart locks everything again
  until a human re-unlocks. This is the exact shape a password manager's server-side vault needs —
  reuse the primitive, don't reinvent it. (Difference from the mailing-list vault: that one holds
  one shared organizational secret; this one needs per-item encryption keyed off a single master
  vault key, still never persisted.)
- **Browser autofill via Chrome extension.** Real scope, not an afterthought — a Manifest V3
  extension that talks to IDUNA over an authenticated session (short-lived token minted after
  vault unlock, not the master passphrase itself ever leaving the vault unlock flow), detects
  login forms, and offers to fill saved credentials. This is what turns "a database with encrypted
  rows" into an actual password manager instead of a vault only usable via CLI/API.
- **CLI access** — matches every other IDUNA-adjacent tool in this codebase
  (`cmd/mailing-list-unlock` is the closest existing precedent): `emily vault get <item>`,
  `emily vault add`, `emily vault unlock`.
- **Sharing / team vaults** — explicitly a later phase (VS2+ below), not needed for "first-class
  product for the founder" at VS0.

## 3. Why IDUNA, not a new service

IDUNA is already the identity/trust authority for every product in this company — the ES256 JWT
issuer, the Apples ledger, the existing (if narrower) vault pattern from the mailing list. A
password manager is fundamentally an identity-adjacent trust surface: "who is allowed to unlock
this, and what do they get back." Standing up a separate service would duplicate the auth model
IDUNA already owns. This extends IDUNA, it doesn't sit next to it.

## 4. Threat model (stated explicitly, not assumed)

- **At-rest disk compromise / leaked backup**: the master key is never written to disk — same
  guarantee the mailing-list vault already gives, extended here.
- **Process memory compromise**: out of scope for v0 — same accepted limitation the existing
  vault has (a sufficiently privileged attacker who can read live process memory can read the
  derived key). Real, worth naming, not solved by this northstar.
- **Server restart**: vault re-locks, matching the existing pattern — a real, live, currently-felt
  operational cost (this exact session hit `iduna.service` restarts re-locking the mailing-list
  vault four separate times) that a password manager built on the same primitive will inherit.
  Worth solving *once*, generally, rather than accepting per-subsystem — see §6.
- **Chrome extension compromise / malicious extension impersonation**: needs a real answer before
  VS1 ships (extension signing, origin-locked messaging, session tokens scoped to short TTLs) —
  flagged as a real design gap for that phase, not solved here.

## 5. Phased plan

**VS0 — founder-only vault, CLI/API access.** New IDUNA tables (`vault_items`, encrypted per-item
via the master vault key), `POST /api/v1/vault/unlock` (mirrors `mailing-list/unlock`'s shape:
loopback-only or agent-authenticated), CRUD endpoints for vault items, `emily vault` CLI commands.
No browser extension yet. This alone would have prevented §1's incident.

**VS1 — Chrome extension.** Manifest V3, autofill detection, session-token auth flow (vault unlock
happens once via CLI/web, extension gets a scoped session token, never touches the master
passphrase or the derived key directly).

**VS2 — team/shared vaults.** Multiple IDUNA users (already exists — Google OAuth humans, per
IDUNA's auth model), shared vault items with per-user access grants. Not scoped further here;
real design work when reopened.

## 6. Open question worth resolving before VS0, not during it

The restart-relocks-the-vault tradeoff (§4) is currently accepted per-subsystem (mailing list
today, this vault tomorrow). Four manual unlocks in one session is a real, measured operational
cost — worth a founder decision on whether that tradeoff still holds at password-manager scale
(where "locked" means the founder can't retrieve *any* saved credential until they SSH in and run
an unlock command), or whether a different key-custody model (e.g., a hardware-backed or
OS-keychain-assisted unlock) is worth the added complexity once this is a daily-use tool rather
than an occasional marketing-list gate. Flagged, not decided, here.

## 7. What this explicitly does not do

Does not compete with or replace 1Password/Bitwarden as an external product — this is an internal
tool built because the founder needs one and the incident in §1 is real. Does not solve browser
extension security in this document (flagged for VS1). Does not attempt biometric/hardware-key
unlock in v0 — master passphrase only, matching the existing vault primitive.
