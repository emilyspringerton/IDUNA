-- S507: Steam F2P onboarding for DEADWEIGHT (founder real-time pivot, 2026-09-21) -- "IDUNA
-- INTEGRATED WOTAN INTEGRATED THE STEAM WILL HANDLE THE ACCOUNTS WE INTEGRATE WITH IT": auth
-- routes through IDUNA (this repo), never a standalone "Wotan IAM" (WOTAN is the static
-- esports-hub site, not an IAM service). Generic across every game in internal/games.Registry,
-- same pattern game_player_stats/game_matches already established -- not DEADWEIGHT-specific
-- naming, so any future game can reuse it via its own games.Config row.

INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-00000000004a', 'deadweight.tickets.write',
   'DEADWEIGHT game server: consume a player''s Draft ticket before allocating a paid match instance');

INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-00000000004a');

-- DEADWEIGHT-SERVER already exists (202609181810); this just grants it the new permission --
-- deliberately NOT granted to DEADWEIGHT-BOTS, same "a bot can never forge a result" separation
-- deadweight.match.write already established (bots must never be able to spend a human's tickets).
INSERT IGNORE INTO agent_permissions (agent_id, permission_id) VALUES
  ('00000003-0000-4000-8000-000000000019', '00000002-0000-4000-8000-00000000004a');

CREATE TABLE IF NOT EXISTS game_player_tickets (
    player_id  CHAR(36)    NOT NULL,
    game       VARCHAR(32) NOT NULL,
    tickets    INTEGER     NOT NULL DEFAULT 0,
    updated_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (player_id, game)
);
