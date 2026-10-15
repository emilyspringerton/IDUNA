# IDUNA Login SDK (SSO)

One contract, two thin client profiles. The password / OAuth ceremony only ever happens on IDUNA's
own hosted page (`/api/v1/auth/sso/login`, served at `https://iam.okemily.com`) — your app never
sees a credential, only an IDUNA-issued JWT.

## The contract

1. Send the user to  
   `GET <base>/api/v1/auth/sso/login?redirect_uri=<absolute URL>`  
   `redirect_uri`'s **host** must be in IDUNA's `SSO_ALLOWED_REDIRECT_HOSTS` (default
   `wotan.okemily.com,localhost,127.0.0.1`); anything else is a plain 400. Put your CSRF `state` in the
   `redirect_uri` query string (it is preserved verbatim).
2. IDUNA signs the user in (Google / Battle.net / Twitch / email) and redirects to  
   `redirect_uri#sso_token=<jwt>&player_id=<id>&display_name=<name>` — a **fragment**, so it is never
   sent to a server or logged.
3. Your app reads the fragment, checks `state`, and uses `sso_token` as `Authorization: Bearer`.
   Tokens are ES256; verify with IDUNA's JWKS at `<base>/.well-known/jwks.json`.

## Profiles

| Profile | File | For |
|---|---|---|
| Browser | `web/iduna-login.js` | web apps (WOTAN pages, EDGE.GAME web, trackers) |
| Native loopback | `c/iduna_login.h` | C/C++/SDL apps (SHANKPIT lobby). Opens the system browser, listens on `127.0.0.1:<ephemeral>`; a tiny `/cb` page forwards the fragment to `/token` on the same listener. Non-blocking `begin()`/`poll()`. Rejects wrong state and non-loopback `Host` headers. |

Test: `cc -D_DEFAULT_SOURCE -Wall -Wextra -pedantic -Werror c/iduna_login_test.c -lpthread && ./a.out`

## Adding a new app

Add its callback host to `SSO_ALLOWED_REDIRECT_HOSTS` (IDUNA deployment env). Native apps need nothing
(loopback is always allowed).

## Known gaps

- Device-flow (`/auth/device/*`) issues a different (HS256, kikoryu-scoped) token; this SDK is the
  ES256 player-JWT path. Unifying the two is tracked separately.
- No refresh: the SSO token is a player JWT with a fixed expiry; re-run login when it lapses.
