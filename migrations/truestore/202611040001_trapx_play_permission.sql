-- TRAPX VS0 web (WOTAN /trapx/, 2026-11-04). Registers the per-game identity scope, mirroring shankpit460.play's
-- exact shape (202610160007_shankpit460_play_permission.sql): only the guest/player play permission, riding the
-- generic internal/games.Registry machinery -- guest accounts, Claim Account, ssoGamePlayer.
--
-- The sandbox's city state lives in the page for now (there is no authoritative city server), so nothing here
-- grants a match-write, checkpoint or bot permission: each of those would be a permission string with no consumer,
-- which is the same deliberately narrow cut big_o's and brawlpit's own rows already document.
INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('9f2c4b16-7a1e-4c33-9d5b-6b0e2f7a4c01', 'trapx.play',
   'Guest player token scope: create/resume a TRAPX citizen identity. Nothing else.');
