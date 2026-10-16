-- shankpit-460 web (WOTAN/shankpit-460/, founder real-time 2026-10-16: "ADD SHANKPIT 460 as a first class citizen to WOTAN
-- ... full IDUNA and WOTAN integration"). Registers the per-game identity scope, mirroring brawlpit.play's exact shape
-- (202609221130_brawlpit_play_permission.sql): only the guest/player play permission, riding the generic
-- internal/games.Registry machinery -- guest accounts, Claim Account, ssoGamePlayer. The human lobby
-- (GET /api/v1/shankpit460/play) resolves its players through that machinery; results/stats are the next pass.
INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000064', 'shankpit460.play',
   'Guest player token scope: create/resume a shankpit-460 account. Nothing else.');
