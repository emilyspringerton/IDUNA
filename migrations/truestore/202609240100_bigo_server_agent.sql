-- EMILY/BACKLOG.md SECTION 536 follow-up, BIG_O/NORTHSTAR.md §27, founder real-time (2026-09-24):
-- "continue with the big_o gfd integration via the phone app". BIG_O now has a real server
-- (day/apps/server) joining the existing GFD<->EINHORN_SURVIVAL chat bridge
-- (GoblinFoxDragon/docs2/CHAT_BRIDGE_TO_EINHORN_SURVIVAL_SPEC.md, internal/http/handlers/
-- chat_messages.go's validChatSources/validChatChannels) as a third real participant, per
-- 202609221100_big_o_play_permission.sql's own explicit "add the rest... when BIG_O actually has
-- a server" note.
--
-- No new permission needed: RequireAuth (any valid IDUNA JWT) is the only gate on
-- POST/GET /api/v1/chat/messages, same real fact GTA7-SERVER/DRAGONSNSHIT-MUD already rely on --
-- confirmed directly (chat_messages.go's own ServeHTTP), not assumed.
--
-- The real agent row itself -- config/agents.json only feeds cmd/bootstrap's own permission-
-- grant/secret-provisioning steps; the row has to actually exist first, same real pattern
-- 202609220930_app_releases.sql's own doc comment already found and documented (bootstrap
-- reports "not found in agents table" otherwise).
INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-00000000001c', '00000000-0000-4000-8000-000000000001',
   'BIGO-SERVER', 'game_server_agent', 'ACTIVE', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
