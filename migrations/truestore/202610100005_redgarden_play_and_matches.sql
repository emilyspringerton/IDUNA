-- REDGARDEN gets the same guest/IDUNA player identity DEADWEIGHT has (founder real-time,
-- 2026-10-10), plus a per-player match log so WOTAN can render a real profile (heroes played,
-- recent matches) instead of just the win/loss counters in player_game_stats.
INSERT OR IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-00000000005b', 'redgarden.play',
   'Guest/IDUNA player token scope: create/resume a REDGARDEN account, mint own connect ticket.');

-- One row per (match, player) as reported by the authoritative arena server. hero_id is the
-- arena hero the player had at match end (-1 = server did not report it).
CREATE TABLE IF NOT EXISTS redgarden_player_matches (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    player_id  TEXT NOT NULL,
    hero_id    INTEGER NOT NULL DEFAULT -1,
    result     TEXT NOT NULL,            -- 'win' | 'loss'
    played_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_rg_pm_player ON redgarden_player_matches (player_id, id DESC);
