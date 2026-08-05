-- GTA7/EINHORN_SURVIVAL Paper plugin agent. Same shape as
-- 202607170003_missing_agents_table_rows.sql -- config/agents.json's own
-- entry (added same day) grants apples.write via cmd/bootstrap's own logic
-- once this agents-table row exists; apples.write is an existing permission
-- (EMILY-PRIME/FATBABY-EMILY/etc. already have it), so no new permissions
-- or role_permissions rows are needed here, unlike the DRAGONSNSHIT-MUD
-- migration which introduced a brand-new permission.

INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-000000000014',
   '00000000-0000-4000-8000-000000000001',
   'GTA7-SERVER', 'game_server_agent', 'ACTIVE',
   CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
