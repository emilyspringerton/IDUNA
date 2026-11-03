---
title: IDUNA
emoji: 🛡️
colorFrom: indigo
colorTo: slate
sdk: docker
app_port: 7860
pinned: false
---

# IDUNA

The identity authority of the EINHORN_INDUSTRIAL stack, shipped as a Hugging Face Space for **testing**:
ES256 JWTs, game-scoped player accounts, Apples, and the SHANKPIT connect-ticket mint.

This Space is a *deployed copy of the repo at one commit*, not a separate build. `deploy/huggingface/push.sh`
in the IDUNA repo assembles it from `git archive` (tracked files only), adds this README and the Dockerfile,
and force-pushes it here. Edit the repo, not this Space.

## What works here

| Endpoint | What it answers |
|---|---|
| `GET /health` | liveness |
| `POST /api/v1/games/shankpit/guest-register` | a real player row + ES256 player token (the Roblox 460 layer's sign-in) |
| `POST /api/v1/games/shankpit/guest-login` | the same account, from the guest secret |
| `POST /api/v1/games/shankpit/guest-upgrade` | link an email onto the SAME player |
| `POST /api/v1/games/shankpit/email-login` | sign that account in from anywhere |
| `POST /api/v1/shankpit/ticket` | a 5-minute, 72-hex connect ticket for the C game server |

## Limits, stated because they matter

- **Free hardware sleeps** after a period of no traffic, and the next request pays a cold start. A ladder
  or a Roblox lobby that pings it will keep it awake; a quiet one will not.
- **Storage is ephemeral.** Every restart, sleep-wake or rebuild is a clean database: guest accounts,
  linked emails and tickets from before it are gone, and the ES256 signing key is regenerated (so tokens
  minted earlier stop verifying). Fine for integration testing; not a home for real accounts.
- **`SHANKPIT_TICKET_SECRET` must be set as a Space secret** and must match the game server's, or every
  ticket this mints is refused at `PACKET_CONNECT`.
- **This is a public login surface.** No default credentials are shipped — `webmaster.json` is excluded —
  but anyone can register a guest account here, exactly as they can against production.
- **No PII is shipped.** `push.sh` refuses to include `*.db`, `var/`, `.env*` or key files even though
  `iduna.db` is tracked in the upstream repo, because a Space repo is public.
