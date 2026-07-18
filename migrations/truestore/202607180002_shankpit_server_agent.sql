-- S156-04: shankpit-460 game server M2M agent + shankpit.match.write
-- permission. The C game server authenticates via POST /api/v1/auth/agent
-- (agent_name + agent_secret) to get a JWT, then uses it to POST match
-- results (kills/deaths) to POST /api/v1/players/{id}/session on match
-- completion. This permission is deliberately NOT granted to ordinary
-- player JWTs — handleSessionEnd trusts its request body with no
-- server-side verification, so only the authoritative source of match
-- results (this agent) may call it; see players.go's
-- shankpitMatchWritePermission comment.

INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000034',
   'shankpit.match.write',
   'Write shankpit-460 match results (kills/deaths) to a player session');

-- super_admin inherits.
INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000034');

INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-000000000011',
   '00000000-0000-4000-8000-000000000001',
   'SHANKPIT460-SERVER', 'game_server_agent', 'ACTIVE',
   CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
