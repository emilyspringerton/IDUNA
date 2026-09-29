-- S513, founder real-time, 2026-09-29: "mixforge add a record button that works on the client
-- side it lets you record the mix and then you can download it or save it to your IDUNA sso
-- account." MIXFORGE (web/dj.html, web/multiplayer.html) had no player-facing IDUNA identity at
-- all before this. Mirrors big_o.play / brawlpit.play's own exact real shape
-- (202609221100_big_o_play_permission.sql, 202609221130_brawlpit_play_permission.sql): only the
-- guest/email-account play permission, riding the same generic internal/games.Registry machinery.
-- No bot/match/checkpoints/tickets permissions -- MIXFORGE has no server-authoritative match
-- concept, and the new mixforge_recordings resource (202609290002) is gated by this same
-- permission rather than a second one.
INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000059', 'mixforge.play',
   'Guest/email player token scope: create/resume a MIXFORGE account, save/list/download own recorded mixes.');
