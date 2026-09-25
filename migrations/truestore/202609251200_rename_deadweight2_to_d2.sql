-- EMILY/BACKLOG.md SECTION 546: founder real-time "just call it D2, disambiguate it from
-- DEADWEIGHT, D2 is the official studio name" -- renames the DEADWEIGHT_2 game/agent identity to
-- D2 (the repo itself is also being renamed, DEADWEIGHT_2 -> D2, but that GitHub-side rename is
-- separately blocked on token permissions as of this migration).
--
-- Never edit an applied migration -- add a new one (this file's own repo convention, IDUNA/
-- CLAUDE.md's "Migrations Checklist"). 202609250900_deadweight2_agents_and_permissions.sql is
-- left exactly as it was; this migration supersedes its two permissions and its one agent by
-- inserting the new (d2.*) rows and removing the old (deadweight_2.*) ones. Safe to do as a clean
-- swap, not a preserve-and-rename: checked directly, no dw2_server has ever run against a live
-- IDUNA instance (this game's own D2 phase was built and verified with --no-auth against a
-- sandboxed IDUNA only) -- nothing real depends on the old deadweight_2.play/deadweight_2.
-- match.write permission names or the DEADWEIGHT2-SERVER agent identity/secret, so there is no
-- live consumer to preserve compatibility for.

INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000057', 'd2.play',
   'Guest player token scope: play D2 and resume own stats. Nothing else.'),
  ('00000002-0000-4000-8000-000000000058', 'd2.match.write',
   'D2 game server (dw2_server): report authoritative match results (updates player ratings)');

INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000058');

INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-00000000001e', '00000000-0000-4000-8000-000000000001',
   'D2-SERVER', 'game_bot_agent', 'ACTIVE', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));

-- Remove the superseded deadweight_2.* rows. role_permissions/agent_permissions rows referencing
-- them are cleaned up first so the deletes never fail on a foreign key.
DELETE FROM role_permissions WHERE permission_id IN ('00000002-0000-4000-8000-000000000055', '00000002-0000-4000-8000-000000000056');
DELETE FROM agent_permissions WHERE permission_id IN ('00000002-0000-4000-8000-000000000055', '00000002-0000-4000-8000-000000000056');
DELETE FROM permissions WHERE id IN ('00000002-0000-4000-8000-000000000055', '00000002-0000-4000-8000-000000000056');
DELETE FROM agent_permissions WHERE agent_id = '00000003-0000-4000-8000-00000000001d';
DELETE FROM agents WHERE id = '00000003-0000-4000-8000-00000000001d';
