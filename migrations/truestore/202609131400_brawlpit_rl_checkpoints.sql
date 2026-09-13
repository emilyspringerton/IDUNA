-- S420: a real, remote, IDUNA-hosted RL checkpoint registry for BRAWLPIT (founder real-time:
-- "lets make a checkpoint registry so we can train from multiple locations and then we can add
-- checkpoints from colab?"). BRAWLPIT/scripts/rl_league.py's own LeagueManager is a real, working
-- registry, but it's a LOCAL filesystem directory -- it only lets separate PROCESSES on the SAME
-- machine share a league. Training from this box AND a Colab runtime (a fresh, ephemeral
-- filesystem every session) needs a real, shared, network-reachable source of truth, the same
-- real reason the BRAWLPIT online level editor (S415-417) needed IDUNA rather than staying a
-- local SQLite file.
--
-- blob_path stores the checkpoint's own real .zip file on disk (var/brawlpit-checkpoints/,
-- named by this row's own id) -- a real binary artifact store, same shape as NOCK's own texture
-- image storage (internal/nock's own PNG-on-disk + SQLite-metadata split), not embedded as a
-- BLOB column (a PPO checkpoint is a few MB; keeping it a plain file is simpler to stream back
-- out via a real, direct GET download than round-tripping through SQLite).
CREATE TABLE IF NOT EXISTS brawlpit_rl_checkpoints (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    role            VARCHAR(32)  NOT NULL,
    generation      INTEGER  NOT NULL,
    elo             REAL     NOT NULL DEFAULT 1500,
    source_location VARCHAR(200) NOT NULL DEFAULT '',
    filename        VARCHAR(200) NOT NULL,
    sha256          VARCHAR(64)  NOT NULL,
    size_bytes      INTEGER  NOT NULL,
    blob_path       VARCHAR(500) NOT NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_brawlpit_rl_checkpoints_role ON brawlpit_rl_checkpoints(role);

-- New permission, agent, and role grant -- mirrors 202607240001_redgarden_bots_agent.sql's own
-- exact real shape. Read (list/download) stays public, same trust level the BRAWLPIT level
-- registry's own GET /api/v1/brawlpit-levels already established -- only uploading a new
-- checkpoint needs a real, authenticated identity.
INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000044',
   'brawlpit.checkpoints.write',
   'Upload a real BRAWLPIT RL checkpoint (.zip) + metadata to the shared, cross-machine checkpoint registry');

INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000044');

INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-000000000016',
   '00000000-0000-4000-8000-000000000001',
   'BRAWLPIT-RL', 'game_bot_agent', 'ACTIVE',
   CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
