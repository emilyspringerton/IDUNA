-- WOTAN for REDGARDEN: a real M2M agent for REDGARDEN's headless bots,
-- mirroring 202607180002_shankpit_server_agent.sql's precedent exactly.
--
-- Two permissions:
--   redgarden.ticket.mint — mint a connect ticket on behalf of an
--     already-registered player_id. Unlike shankpit's ShankpitTicketHandler
--     (which mints for the CALLER's own player_id, from a real human's
--     OAuth-issued JWT sub), REDGARDEN bots have no OAuth login at all —
--     they authenticate as this agent and mint on behalf of a player_id
--     supplied in the request body. RedgardenTicketHandler additionally
--     restricts this to players registered under provider='redgarden_bot',
--     so this permission can never mint a ticket impersonating a real
--     human player even if the agent secret leaked.
--   redgarden.match.write — write REDGARDEN match results (win/loss) to
--     player_game_stats. Same trust model as shankpit.match.write:
--     RedgardenGameResultHandler trusts its request body with no
--     server-side verification beyond the permission check, so only the
--     authoritative source of match results (this agent, or the game
--     server itself once it's granted the same permission) may call it.

INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000035',
   'redgarden.ticket.mint',
   'Mint a REDGARDEN connect ticket on behalf of an already-registered redgarden_bot player'),
  ('00000002-0000-4000-8000-000000000036',
   'redgarden.match.write',
   'Write REDGARDEN match results (win/loss) to a player''s game stats');

-- super_admin inherits.
INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000035'),
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000036');

INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-000000000012',
   '00000000-0000-4000-8000-000000000001',
   'REDGARDEN-BOTS', 'game_bot_agent', 'ACTIVE',
   CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
