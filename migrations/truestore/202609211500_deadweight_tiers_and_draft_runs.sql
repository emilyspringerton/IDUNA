-- S508c: DEADWEIGHT economy correction (founder real-time, same day, superseding S508b's flat
-- "+1 ticket/day" with a tiered daily TOP-UP-TO-CAP, and replacing per-match ticket spend with an
-- UNCAPPED DRAFT RUN model: 1 ticket buys a run with no win cap, the run ends strictly at 3
-- losses, and the final win count is posted to a real leaderboard). Generic across game.Registry,
-- same convention every other game_* table already follows.

-- Renamed from last_free_ticket_at (202609211400) -- same column, new semantic: the last time
-- this player's tier-cap top-up ran, not "the last flat +1 grant."
ALTER TABLE game_player_tickets RENAME COLUMN last_free_ticket_at TO last_ticket_topup_at;
ALTER TABLE game_player_tickets ADD COLUMN tier VARCHAR(16) NOT NULL DEFAULT 'tier_alpha';

-- One row per player = their current, in-progress Draft Run (active=0 rows are just history of a
-- player who has drafted before but has no run going right now -- kept rather than deleted so
-- the PRIMARY KEY upsert in draft-run/start has something to ON CONFLICT against).
CREATE TABLE IF NOT EXISTS game_draft_runs (
    player_id  CHAR(36)    NOT NULL,
    game       VARCHAR(32) NOT NULL,
    active     INTEGER     NOT NULL DEFAULT 0,
    wins       INTEGER     NOT NULL DEFAULT 0,
    losses     INTEGER     NOT NULL DEFAULT 0,
    started_at DATETIME,
    PRIMARY KEY (player_id, game)
);

-- Completed runs -- the real "Uncapped Draft Runs" leaderboard the founder's own marketing hook
-- ("Twitch streamers will lose their minds trying to break the 30-win barrier") needs a query
-- surface for. Append-only; a player's best run is a MAX(wins) query, not a mutated row.
CREATE TABLE IF NOT EXISTS game_draft_run_results (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    player_id CHAR(36)    NOT NULL,
    game      VARCHAR(32) NOT NULL,
    wins      INTEGER     NOT NULL,
    ended_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_draft_run_results_wins ON game_draft_run_results(game, wins DESC);

-- Scrappy anti-bot for guest-register (founder: "Max 3 new accounts per IP address per 24
-- hours. No email verification required yet.") -- a real 24h window, deliberately NOT the
-- existing per-minute token-bucket IPRateLimiter (GameOnlineHandler.Limiter), which refills
-- continuously and can't express "3 total in a day." A separate, narrow check.
CREATE TABLE IF NOT EXISTS game_signup_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    ip         VARCHAR(64) NOT NULL,
    game       VARCHAR(32) NOT NULL,
    created_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_game_signup_log_ip ON game_signup_log(ip, game, created_at);

-- Claim codes can now also grant a tier upgrade (e.g. a Founder pack bumping tier_free ->
-- tier_premium), alongside the existing tickets/founder_flag grants -- optional, NULL = no
-- tier change.
ALTER TABLE game_claim_codes ADD COLUMN tier VARCHAR(16);
