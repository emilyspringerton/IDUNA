-- SHANKPIT_OS_NORTHSTAR.md's own real capability audit, item 4: "BRAWLPIT has no player-facing
-- IDUNA identity integration at all -- checked directly, no brawlpit_auth-equivalent handler
-- exists; the only BRAWLPIT-IDUNA surface found is agent-auth-gated checkpoint upload
-- (brawlpit_checkpoints.go, M2M only, not a player login)." Named there as real, genuinely new
-- work independent of the shell-surface open question (Option A/B/C) -- "BRAWLPIT can be a menu
-- item" needs an actual account to launch under regardless of which shell option is picked.
--
-- Mirrors big_o.play's own exact real shape (202609221100_big_o_play_permission.sql): only the
-- guest-account play permission, riding the same generic internal/games.Registry machinery
-- DEADWEIGHT and BIG_O already use. No bot/match/checkpoints permissions here -- checkpoints.write
-- already exists as brawlpit.checkpoints.write (202609131400), a real, separate, already-live
-- M2M-only permission this does NOT touch or replace.
INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000054', 'brawlpit.play',
   'Guest player token scope: create/resume a BRAWLPIT account. Nothing else.');
