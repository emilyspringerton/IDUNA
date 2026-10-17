-- REDGARDEN web (WOTAN /redgarden/play.html, founder real-time 2026-10-17: "FULL WOTAN MATCH AND PLAYER TRACKING"):
-- one row per match a signed-in or guest player played through the browser gateway (internal/redgardenplay), written by
-- the gateway from the arena's own authoritative snapshots. This is the full record; the win/loss counters, the hero log
-- and the hero win rates WOTAN's leaderboard already reads (player_game_stats, redgarden_player_matches,
-- redgarden_hero_stats) are updated in the same transaction for every match that counts.
CREATE TABLE IF NOT EXISTS redgarden_web_matches (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    player_id   TEXT    NOT NULL,
    pool        TEXT    NOT NULL DEFAULT '',        -- which bot pool hosted it (rnd-3v3 | rnd-10v10 | stable-10v10)
    mode        TEXT    NOT NULL DEFAULT '3v3',
    lobby       INTEGER NOT NULL DEFAULT 0,         -- players in the match, humans + bots
    slot        INTEGER NOT NULL DEFAULT 0,         -- the player's hero slot
    team        INTEGER NOT NULL DEFAULT 0,
    hero_id     INTEGER NOT NULL DEFAULT -1,
    result      TEXT    NOT NULL,                   -- win | loss | abandoned | aborted
    winner_team INTEGER NOT NULL DEFAULT -1,
    kills       INTEGER NOT NULL DEFAULT 0,
    deaths      INTEGER NOT NULL DEFAULT 0,
    flow_earned INTEGER NOT NULL DEFAULT 0,
    xp          INTEGER NOT NULL DEFAULT 0,
    seconds     INTEGER NOT NULL DEFAULT 0,
    counted     INTEGER NOT NULL DEFAULT 0,         -- 1 = it moved the win/loss counters
    roster      TEXT    NOT NULL DEFAULT '[]',      -- JSON: [{slot,team,hero,kills,deaths,you}]
    played_at   TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_rg_wm_player ON redgarden_web_matches (player_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_rg_wm_recent ON redgarden_web_matches (id DESC);
