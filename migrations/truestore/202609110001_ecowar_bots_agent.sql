-- ECOWAR headless bots + matchmaker-spawned arena_server agent. Same shape as
-- 202608050002_gta7_server_agent.sql -- config/agents.json's own entry
-- (added same day) grants redgarden.ticket.mint + redgarden.match.write via
-- cmd/bootstrap's own logic once this agents-table row exists; both are
-- existing permissions (REDGARDEN-BOTS already has them, created by
-- 202607240001_redgarden_bots_agent.sql), so no new permissions or
-- role_permissions rows are needed here.
--
-- A genuinely DISTINCT agent identity from REDGARDEN-BOTS, not a reused
-- credential -- real root-cause fix for ecowar-matchmaker.service failing to
-- start ("Failed to load environment files") because
-- var/redgarden-iduna-agent.env was never provisioned for ECOWAR specifically
-- (ECOWAR/README.md's own "does ECOWAR report under its own agent identity or
-- REDGARDEN's? -- flagged, not decided" question, resolved here: its own
-- identity, so ECOWAR match stats attribute to ECOWAR on WOTAN, never mixed
-- into REDGARDEN's own leaderboard).

INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-000000000015',
   '00000000-0000-4000-8000-000000000001',
   'ECOWAR-BOTS', 'game_bot_agent', 'ACTIVE',
   CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
