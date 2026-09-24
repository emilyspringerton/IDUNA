-- S537 Duel Phase 2, Phase 1 (IDUNA side): accepting a duel mints a short-lived shared match
-- token both players can present to their game's own matchmaking queue to be paired together
-- specifically, instead of the queue's normal FIFO/random pairing. Investigated first (Principle
-- 19): no per-game matchmaker in this monorepo (DEADWEIGHT, REDGARDEN, ECOWAR, SHANKPIT,
-- BRAWLPIT) has any existing concept of "pair me with player X specifically" -- this token is the
-- new, minimal primitive each game's own queue can key off of. TTL-bounded (same pattern as
-- ShankpitMatchedTTL, game_social.go's own duelRespond sets the expiry) so a stale accepted duel
-- from days ago can't be replayed into a fresh match indefinitely.

ALTER TABLE duel_challenges ADD COLUMN match_token TEXT;
ALTER TABLE duel_challenges ADD COLUMN match_token_expires_at DATETIME;
