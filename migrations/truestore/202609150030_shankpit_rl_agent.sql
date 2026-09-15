-- S459-50, real follow-up in the same session as 202609150022_shankpit_rl_checkpoints.sql:
-- "ensure we have the colab training skrip" -- scripts/colab_train.py + scripts/rl_registry.py
-- need a real M2M agent to authenticate a checkpoint push as, mirroring BRAWLPIT-RL's own exact
-- real shape (202609131400_brawlpit_rl_checkpoints.sql). A SEPARATE migration from
-- 202609150022 because that one was already applied (recorded in schema_migrations) by the time
-- this need was found -- never modify an applied migration.
--
-- cmd/bootstrap (idempotent, run with no -rotate) provisions the actual secret from this row +
-- its matching config/agents.json entry on next run -- not set here.
INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-000000000017',
   '00000000-0000-4000-8000-000000000001',
   'SHANKPIT-RL', 'game_bot_agent', 'ACTIVE',
   CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
