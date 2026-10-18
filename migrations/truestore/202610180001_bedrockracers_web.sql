-- BEDROCK RACERS web (WOTAN /bedrockracers/, founder real-time 2026-10-18: "SHIP IT TO WOTAN AS AN ESPORT ... STATS ALL OF IT NOT JUST AGAINST BOTS THIS IS
-- MULTIPLAYER"). The per-game identity scope (guest accounts, Claim Account and ssoGamePlayer ride the generic internal/games.Registry machinery), then what the
-- referee (internal/bedrockracers: the lockstep room that steps the SAME wasm the browsers play) records. The rating / win / loss counters WOTAN's ladder reads
-- live in game_player_stats for game 'bedrockracers' (the generic leaderboard route serves them); a match has four teams, so the rating update is the room
-- recorder's own (handlers/bedrockracers_play.go), not the two-seat applyGameMatch.
INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000066', 'bedrockracers.play',
   'Guest player token scope: create/resume a Bedrock Racers account. Nothing else.');

-- One row per match the room played to the end (or to its decree). roster is a JSON array with one entry per car (people and bots alike):
-- slot, team, player_id, name, kind (human | bot | anon), left, left_tick, and what the module counted for that car.
CREATE TABLE IF NOT EXISTS bedrockracers_matches (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    mode        TEXT    NOT NULL DEFAULT 'lobby',
    seed        INTEGER NOT NULL DEFAULT 0,
    sim_version INTEGER NOT NULL DEFAULT 0,            -- the rules the match ran on; a replay needs the same
    level       INTEGER NOT NULL DEFAULT 2,            -- the bots' skill: 1 easy | 2 normal | 3 hard
    humans      INTEGER NOT NULL DEFAULT 0,            -- people at the start
    winner      INTEGER NOT NULL DEFAULT -1,           -- team 0..3, -1 none
    reason      INTEGER NOT NULL DEFAULT 0,            -- 1 goal limit | 2 time | 3 golden goal | 4 closed by decree
    ticks       INTEGER NOT NULL DEFAULT 0,            -- 62.5 a second, countdown not counted
    overtime    INTEGER NOT NULL DEFAULT 0,
    score0      INTEGER NOT NULL DEFAULT 0,
    score1      INTEGER NOT NULL DEFAULT 0,
    score2      INTEGER NOT NULL DEFAULT 0,
    score3      INTEGER NOT NULL DEFAULT 0,
    counted     INTEGER NOT NULL DEFAULT 0,            -- 1 = it moved the ladder
    desyncs     INTEGER NOT NULL DEFAULT 0,            -- browsers whose state hash disagreed with the referee's
    roster      TEXT    NOT NULL DEFAULT '[]',
    replay      BLOB,                                  -- header + the deflated TICK stream (see internal/bedrockracers/room.go replayBlob)
    played_at   TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_br_m_recent ON bedrockracers_matches (id DESC);

-- One row per person with an account per match: what their car did, and what the ladder did to them.
CREATE TABLE IF NOT EXISTS bedrockracers_player_matches (
    match_id      INTEGER NOT NULL,
    player_id     TEXT    NOT NULL,
    slot          INTEGER NOT NULL DEFAULT 0,
    team          INTEGER NOT NULL DEFAULT 0,
    result        TEXT    NOT NULL,                    -- win | loss | draw | left
    goals         INTEGER NOT NULL DEFAULT 0,
    assists       INTEGER NOT NULL DEFAULT 0,
    own_goals     INTEGER NOT NULL DEFAULT 0,
    steals        INTEGER NOT NULL DEFAULT 0,
    catches       INTEGER NOT NULL DEFAULT 0,
    throws        INTEGER NOT NULL DEFAULT 0,
    passes        INTEGER NOT NULL DEFAULT 0,
    fires         INTEGER NOT NULL DEFAULT 0,
    touches       INTEGER NOT NULL DEFAULT 0,
    top_speed     INTEGER NOT NULL DEFAULT 0,
    air_used      INTEGER NOT NULL DEFAULT 0,
    humans        INTEGER NOT NULL DEFAULT 0,
    rating_before REAL    NOT NULL DEFAULT 0,
    rating_after  REAL    NOT NULL DEFAULT 0,
    counted       INTEGER NOT NULL DEFAULT 0,
    played_at     TEXT    NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (match_id, player_id)
);
CREATE INDEX IF NOT EXISTS idx_br_pm_player ON bedrockracers_player_matches (player_id, match_id DESC);
