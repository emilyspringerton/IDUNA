-- Kanban card 123214231 (founder real-time): "we need a big_o account creation interface off of
-- iduna". BIG_O (docs/NORTHSTAR.md only, no server/client code yet -- see BIG_O/NORTHSTAR.md)
-- gets its own guest-account identity on the SAME generic internal/games.Registry machinery
-- DEADWEIGHT already uses, per that package's own real doc comment: "adding a game with guest
-- accounts... is one row here (plus a migration granting its permissions/agents), not a new
-- handler." players/game_guest_credentials/game_signup_log/player_credentials/
-- game_player_tickets are already shared, game-agnostic tables (keyed by a `game` column) -- no
-- new table needed here at all, just the one permission a guest token needs to carry.
--
-- Deliberately narrow: only big_o.play, not bot/match/checkpoints permissions too. BIG_O has no
-- server or bot agent yet to hold them -- adding permissions with no real consumer would be
-- speculative scope, not this card's actual ask. Add the rest (mirroring
-- 202609181810_deadweight_agents_and_stats.sql's own shape) when BIG_O actually has a server.
INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000053', 'big_o.play',
   'Guest player token scope: create/resume a BIG_O account. Nothing else.');
