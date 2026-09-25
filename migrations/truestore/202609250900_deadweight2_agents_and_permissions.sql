-- EMILY/BACKLOG.md SECTION 546 D2 ("server-authoritative 1v1"), DEADWEIGHT_2/NORTHSTAR.md's own
-- deferred item: "its own M2M agent identity (never reusing DEADWEIGHT's or ECOWAR's -- ECOWAR-
-- BOTS's own precedent)". players/game_guest_credentials/game_player_stats/game_matches are
-- already shared, game-agnostic tables (keyed by a `game` column, from
-- 202609181810_deadweight_agents_and_stats.sql) -- no new table needed, same real conclusion
-- 202609221100_big_o_play_permission.sql's own doc comment already reached for big_o.
--
-- Deliberately narrow, mirroring that same big_o precedent: only deadweight_2.play (guest player
-- token scope) and deadweight_2.match.write (dw2_server reporting authoritative results). No
-- deadweight_2.bot.play / DEADWEIGHT2-BOTS agent yet -- D4 ("a real placeholder bot") is its own,
-- separate, not-yet-built phase; minting a bot identity with no bot to hold it would be
-- speculative scope, not this migration's actual job. Add it, mirroring this same shape, once D4
-- lands. Agent -> permission grants come from config/agents.json via cmd/bootstrap (same as every
-- other game agent).

INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000055', 'deadweight_2.play',
   'Guest player token scope: play DEADWEIGHT_2 and resume own stats. Nothing else.'),
  ('00000002-0000-4000-8000-000000000056', 'deadweight_2.match.write',
   'DEADWEIGHT_2 game server (dw2_server): report authoritative match results (updates player ratings)');

INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000056');

INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-00000000001d', '00000000-0000-4000-8000-000000000001',
   'DEADWEIGHT2-SERVER', 'game_bot_agent', 'ACTIVE', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
