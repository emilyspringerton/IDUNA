-- Genre-agnostic per-game stats, separate from the FPS-shaped `players`
-- table (kills/deaths/sessions). REDGARDEN NORTHSTAR §12 Phase A flagged
-- this exact decision: forcing REDGARDEN's win/loss results into shankpit's
-- kills/deaths columns would corrupt shared WOTAN profile semantics across
-- every game using that table. One row per (player_id, game) instead, so
-- each game reports results shaped the way that game actually works.

CREATE TABLE IF NOT EXISTS player_game_stats (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    player_id       CHAR(36)    NOT NULL,
    game            VARCHAR(32) NOT NULL,   -- "redgarden", future: "shankpit", "gfd-mud", etc.
    wins            INTEGER     NOT NULL DEFAULT 0,
    losses          INTEGER     NOT NULL DEFAULT 0,
    matches_played  INTEGER     NOT NULL DEFAULT 0,
    last_played_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(player_id, game)
);
CREATE INDEX IF NOT EXISTS idx_player_game_stats_game_wins ON player_game_stats(game, wins);
