-- S428: "i want to reset training but not include certain models from the registry - can you
-- add a checkbox to the registry backend to disable those models from the league?" (founder
-- real-time). A real, per-checkpoint "excluded from the league" flag -- distinct from
-- is_active_opponent (one global in-game opponent selection): this one marks a checkpoint as
-- something training/bot-pool/league logic should skip entirely (won't be picked as a
-- --resume-from-registry warm-start, won't be drawn into the bot pool), while still leaving the
-- row and its blob intact in the registry for later re-enabling or manual inspection -- a real,
-- reversible exclusion, not a delete.
ALTER TABLE brawlpit_rl_checkpoints ADD COLUMN is_disabled INTEGER NOT NULL DEFAULT 0;
