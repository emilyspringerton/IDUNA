-- shankpit-460 web results (founder real-time 2026-11-15: "we need esports and stats up asap ... start tracking and training bots asap for our various
-- esports leagues"). The matchmaker (internal/shankpit460) referees free-for-all rooms of up to ten; every match that goes live is recorded here by
-- handlers/shankpit460_stats.go. The rating / win / loss counters WOTAN's ladder reads live in game_player_stats for game 'shankpit460' (the generic
-- leaderboard route serves them); a match has up to ten people, so the rating update is a pairwise Elo across the finishing order, not applyGameMatch.

-- One row per match that went live. roster is a JSON array, one entry per person in finishing order (guests included):
-- slot, player_id (empty for a guest), name, kind (human | guest), kills, deaths, place, left, left_after_ms, result, rating_before, rating_after.
CREATE TABLE IF NOT EXISTS shankpit460_matches (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    match_uid    TEXT    NOT NULL UNIQUE,              -- when it went live + the room: one row per match even if recording is retried
    room_id      INTEGER NOT NULL DEFAULT 0,
    reason       INTEGER NOT NULL DEFAULT 0,           -- 1 kill limit | 2 time | 3 abandoned
    kill_limit   INTEGER NOT NULL DEFAULT 0,
    live_ms      INTEGER NOT NULL DEFAULT 0,
    players      INTEGER NOT NULL DEFAULT 0,           -- everyone who played, leavers included
    signed_in    INTEGER NOT NULL DEFAULT 0,           -- of whom had an account
    winner_name  TEXT    NOT NULL DEFAULT '',
    winner_id    TEXT    NOT NULL DEFAULT '',          -- the winner's player row; empty for a guest winner or no winner
    top_kills    INTEGER NOT NULL DEFAULT 0,
    counted      INTEGER NOT NULL DEFAULT 0,           -- 1 = it moved the ladder
    roster       TEXT    NOT NULL DEFAULT '[]',
    played_at    TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_sp460_m_recent ON shankpit460_matches (id DESC);

-- One row per person with an account per match: what they did, and what the ladder did to them.
CREATE TABLE IF NOT EXISTS shankpit460_player_matches (
    match_id      INTEGER NOT NULL,
    player_id     TEXT    NOT NULL,
    slot          INTEGER NOT NULL DEFAULT 0,
    place         INTEGER NOT NULL DEFAULT 0,
    players       INTEGER NOT NULL DEFAULT 0,
    result        TEXT    NOT NULL,                    -- win | loss | draw | left
    kills         INTEGER NOT NULL DEFAULT 0,
    deaths        INTEGER NOT NULL DEFAULT 0,
    rating_before REAL    NOT NULL DEFAULT 0,
    rating_after  REAL    NOT NULL DEFAULT 0,
    counted       INTEGER NOT NULL DEFAULT 0,
    played_at     TEXT    NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (match_id, player_id)
);
CREATE INDEX IF NOT EXISTS idx_sp460_pm_player ON shankpit460_player_matches (player_id, match_id DESC);
