-- REDGARDEN_GUI_NORTHSTAR.md Milestone 3: a real M2M agent for GoblinFoxDragon's apps2/mud
-- (DragonsNShit telnet MUD), mirroring 202607240001_redgarden_bots_agent.sql's precedent but
-- for the OPPOSITE trust direction.
--
-- redgarden.player-ticket.mint — mint a REDGARDEN connect ticket on behalf of a real
--   DragonsNShit character's own player_id. apps2/mud has no OAuth login of its own (a real,
--   separate, undesigned question -- see REDGARDEN_GUI_NORTHSTAR.md), so like REDGARDEN-BOTS'
--   own redgarden.ticket.mint it mints on behalf of a player_id supplied in the request body,
--   not the caller's own JWT. Deliberately a SEPARATE permission rather than widening
--   redgarden.ticket.mint's own grant: RedgardenPlayerTicketHandler requires the player_id to
--   have a real `characters` row (a real DragonsNShit identity) instead of
--   provider='redgarden_bot', the exact opposite scoping -- so neither permission can be used
--   to satisfy the other's trust model, even if one agent's secret leaked.

INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000037',
   'redgarden.player-ticket.mint',
   'Mint a REDGARDEN connect ticket on behalf of a real DragonsNShit character''s own player_id');

-- super_admin inherits.
INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000037');

INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-000000000013',
   '00000000-0000-4000-8000-000000000001',
   'DRAGONSNSHIT-MUD', 'game_server_agent', 'ACTIVE',
   CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
