-- S421-02: founder real-time -- "ensure that the client actually uses that model (download it
-- when the game starts...)" + "make sure that the models have like some id number or something
-- to identify them maybe datestamp to the second with the archetype type" + "make sure the model
-- downloads with lz4".
--
-- name: a real, human-readable identifier -- "<role>_<YYYYMMDD>_<HHMMSS>" (UTC, to the second),
-- generated server-side at Create() time from the row's own real created_at, never trusted from
-- the uploader -- guarantees global uniqueness without depending on a client's own clock, and
-- reads immediately meaningfully in the AI Opponents UI / the in-game HUD, unlike the bare
-- integer `id` alone.
--
-- weights_blob_path/weights_size_bytes/weights_sha256: the real, exported MLP inference weights
-- (scripts/export_policy_weights.py's own "BPMW" binary format) for THIS SAME checkpoint --
-- a real, separate artifact from the .zip (which stays the resumable stable_baselines3 training
-- state; the weights blob is the small, portable, native-client-loadable one). Nullable: an
-- older checkpoint uploaded before this migration, or one never exported, legitimately has none.
ALTER TABLE brawlpit_rl_checkpoints ADD COLUMN name VARCHAR(200) NOT NULL DEFAULT '';
ALTER TABLE brawlpit_rl_checkpoints ADD COLUMN weights_blob_path VARCHAR(500) NOT NULL DEFAULT '';
ALTER TABLE brawlpit_rl_checkpoints ADD COLUMN weights_size_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE brawlpit_rl_checkpoints ADD COLUMN weights_sha256 VARCHAR(64) NOT NULL DEFAULT '';
