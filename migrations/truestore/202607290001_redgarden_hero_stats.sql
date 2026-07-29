-- Aggregate per-hero win/loss, separate from player_game_stats (which is keyed by player_id,
-- not hero_id -- a hero's own strength is a cross-player aggregate, "how often does anyone
-- playing Gary win," not tied to any one account). Founder: "ok i want to start tracking it on
-- okemily.com" / "can we start crunching the data on the heroes that are the strongest" --
-- REDGARDEN's own local match logs (var/matches/*.jsonl) can compute this offline
-- (scripts/hero_stats.py), but a real public page on okemily.com needs a real, durable,
-- always-on data source, not a file this box's own filesystem happens to have -- same
-- "player_game_stats vs. shankpit's own kills/deaths columns" genre-shape reasoning
-- 202607240002_player_game_stats.sql's own comment already gives, applied one level up: hero_id
-- numbering is entirely REDGARDEN's own roster (packages/simulation/arena_game.h's
-- ArenaHeroID), so this table is REDGARDEN-specific by construction, same as
-- player_game_stats.game = "redgarden" rows are today, just without a separate `game` column
-- since there's only ever one roster this hero_id space could mean.

CREATE TABLE IF NOT EXISTS redgarden_hero_stats (
    hero_id         INTEGER  PRIMARY KEY,
    wins            INTEGER  NOT NULL DEFAULT 0,
    losses          INTEGER  NOT NULL DEFAULT 0,
    matches_played  INTEGER  NOT NULL DEFAULT 0,
    last_played_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
