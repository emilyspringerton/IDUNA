-- D2 (DEADWEIGHT_2) web (WOTAN /d2/play.html, founder real-time 2026-10-17: "now DEADWEIGHT_2 same affordances"): one row per match a
-- signed-in or guest player played through the browser gateway (internal/d2play), written by the gateway from the match server's own
-- authoritative frames and the player's packed ship. The Elo / win / loss counters WOTAN's ladder reads (game_player_stats and
-- game_matches for game 'd2') are moved in the same transaction by the shared applyGameMatch for every match that counts.
CREATE TABLE IF NOT EXISTS d2_web_matches (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    player_id     TEXT    NOT NULL,
    opp_player_id TEXT    NOT NULL DEFAULT '',        -- the bot's or the human's d2 player id ('' for a human off this ladder)
    opp_name      TEXT    NOT NULL DEFAULT '',
    opp_kind      TEXT    NOT NULL DEFAULT 'bot',     -- bot | human
    match_id      INTEGER NOT NULL DEFAULT 0,         -- dw2_server's id (restarts from 1; (match_id, seed) is the identity)
    seed          INTEGER NOT NULL DEFAULT 0,
    seat          INTEGER NOT NULL DEFAULT 0,
    result        TEXT    NOT NULL,                   -- win | loss | tie | forfeit | aborted
    reason        TEXT    NOT NULL DEFAULT '',        -- hull | timeout | forfeit | server
    ticks         INTEGER NOT NULL DEFAULT 0,
    hull_you      INTEGER NOT NULL DEFAULT 0,
    hull_opp      INTEGER NOT NULL DEFAULT 0,
    armor_you     INTEGER NOT NULL DEFAULT 0,
    armor_opp     INTEGER NOT NULL DEFAULT 0,
    cargo_you     INTEGER NOT NULL DEFAULT 0,
    cargo_opp     INTEGER NOT NULL DEFAULT 0,
    seconds       INTEGER NOT NULL DEFAULT 0,
    loadout       TEXT    NOT NULL DEFAULT '[]',      -- JSON: [{item,row,col,rot,cut}] in placement order
    rounds        TEXT    NOT NULL DEFAULT '[]',      -- JSON: the round-break mini-game, round by round
    rating_before REAL    NOT NULL DEFAULT 0,
    rating_after  REAL    NOT NULL DEFAULT 0,
    counted       INTEGER NOT NULL DEFAULT 0,         -- 1 = it moved the ladder
    played_at     TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_d2_wm_player ON d2_web_matches (player_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_d2_wm_recent ON d2_web_matches (id DESC);
