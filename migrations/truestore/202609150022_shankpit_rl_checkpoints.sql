-- S459-49, founder real-time: "bring in the bot registry affordances on NOCK all the same -
-- ability to disable - hide disabled - set default (defer this put the button then put like a
-- daisy ui alert not implemented) - for shankpit". Mirrors BRAWLPIT's own real, shared
-- checkpoint registry (202609131400/1500/1700_brawlpit_*.sql, internal/brawlpit/
-- checkpoint_store.go) field-for-field, consolidated into one migration since this table starts
-- fresh rather than growing through the same organic multi-session history BRAWLPIT's own did.
--
-- Real, deliberate scope-down from BRAWLPIT's own final schema: no weights_* columns (BRAWLPIT's
-- own native-inference weight export, scripts/export_policy_weights.py, has no SHANKPIT
-- equivalent yet -- a real, separate, larger piece of future work, not touched here) and no
-- match-result/Elo-update endpoint (nothing yet calls it -- SHANKPIT's own training pipeline,
-- scripts/rl_train_packet.py per S459-48, doesn't push to a remote registry yet either). `elo`
-- stays a real column (defaulting 1500, matching BRAWLPIT's own real starting rating) since a
-- future real registry-push integration will want it -- it just won't move from its inherited
-- value until that match-result mechanism exists, same honest status this table's own real
-- population (currently zero rows) already has.
CREATE TABLE IF NOT EXISTS shankpit_rl_checkpoints (
    id                 INTEGER  PRIMARY KEY AUTOINCREMENT,
    name               VARCHAR(200) NOT NULL DEFAULT '',
    role               VARCHAR(32)  NOT NULL,
    generation         INTEGER  NOT NULL,
    elo                REAL     NOT NULL DEFAULT 1500,
    source_location    VARCHAR(200) NOT NULL DEFAULT '',
    filename           VARCHAR(200) NOT NULL,
    sha256             VARCHAR(64)  NOT NULL,
    size_bytes         INTEGER  NOT NULL,
    blob_path          VARCHAR(500) NOT NULL,
    is_active_opponent INTEGER  NOT NULL DEFAULT 0,
    is_disabled        INTEGER  NOT NULL DEFAULT 0,
    created_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_shankpit_rl_checkpoints_role ON shankpit_rl_checkpoints(role);

-- New permission, mirrors brawlpit.checkpoints.write's own exact real shape -- gates the one
-- write action a training pipeline would use to push a checkpoint (upload). No new agent row is
-- created here (unlike BRAWLPIT-RL): no automated SHANKPIT training pipeline pushes to a remote
-- registry yet (S459-48's own rl_train_packet.py stays local-checkpoint-only for now) -- real,
-- honestly deferred rather than provisioning credentials for a consumer that doesn't exist.
INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000045',
   'shankpit.checkpoints.write',
   'Upload a real SHANKPIT RL checkpoint (.zip) + metadata to the shared checkpoint registry');

INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000045');
