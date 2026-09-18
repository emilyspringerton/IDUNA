-- S503-06: generalize the BRAWLPIT checkpoint registry into a game-scoped one instead of copying it a
-- third time (shankpit already has its own copy). Every existing row is a brawlpit row, so the default
-- keeps every existing route/row behaving exactly as before; new games (deadweight) get their own
-- `game` value and are isolated from each other by every store query.
ALTER TABLE brawlpit_rl_checkpoints ADD COLUMN game VARCHAR(32) NOT NULL DEFAULT 'brawlpit';
CREATE INDEX IF NOT EXISTS idx_brawlpit_rl_checkpoints_game ON brawlpit_rl_checkpoints(game, role);
