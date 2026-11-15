IDUNA -- run anywhere
=====================

IDUNA is EINHORN_INDUSTRIAL's sign-in, accounts and game-stats server. This zip is the whole server: one program
plus the files it serves. No cloud account is needed.

Run it
  Windows:  double-click RUN.bat
  Linux:    ./RUN.sh
RUN opens the WOTAN arcade at http://127.0.0.1:8090/ : the games, sign-in, leaderboards and matchmaking, all served by
this one program. http://localhost:8080/health answers {"ok":true,...} when IDUNA itself is up.
Everything it prints goes to iduna.log next to it.

What plays here
  BRAWLPIT, shankpit-460 and Bedrock Racers run in the browser (their bots too). shankpit-460's PLAY starts a bot match
  and searches for another human: two people searching on this IDUNA are put in one free-for-all lobby. Bedrock Racers'
  online rooms run on this IDUNA as well. Red Garden, D2 and DEADWEIGHT need their own game servers and bot pools,
  which are not in this zip: their pages say they are offline.

Settings
  None are required. To change one, copy iduna.env.example to iduna.env and edit it (one NAME=value per line).
  Out of the box it listens on 127.0.0.1:8080 and the arcade on 127.0.0.1:8090 (this computer only). To play from a
  phone or another computer on your network: IDUNA_ADDR=0.0.0.0:8080, WOTAN_ADDR=0.0.0.0:8090, and add this
  computer's address to SSO_ALLOWED_REDIRECT_HOSTS.

What works with no settings
  Accounts (guest and email/password), each game's guest accounts and sign-in, the game routes the WOTAN pages call
  (leaderboards, matches, shankpit-460 matchmaking, Redeem a code), and the Back Office pages.

What needs a setting
  Google sign-in: GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET.
  The Red Garden web gateway: REDGARDEN_TICKET_SECRET, the same value the arena servers use.
  Log ingest (/services/collector): IDUNA_HEC_TOKEN.
  NOCK's compile tools need gcc, ImageMagick and the PARENA compiler on the machine. Those come with the container
  image (ghcr.io/emilyspringerton/iduna), not with this zip.

The first admin account
  Before the first run, create var/webmaster.json with three fields:
    {"email": "...", "password": "...", "display_name": "..."}
  IDUNA uses it once, at first start, to create account 0. Delete the file afterwards.

Your data, back up both
  var/iduna.db      the database (SQLite).
  iduna-key.json    the key that signs every sign-in token. Anyone holding it can sign in as anyone. Keep it private.

Updating
  Stop IDUNA, unzip the new version over this folder (var/ and iduna-key.json are left alone), and start it again.
  New database migrations run at startup.
