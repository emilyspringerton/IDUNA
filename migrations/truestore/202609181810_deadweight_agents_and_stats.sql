-- S503-06: DEADWEIGHT online services -- distinct agent identities (never REDGARDEN's/ECOWAR's, so match stats
-- never mix; ECOWAR-BOTS precedent), permissions, guest credentials, per-player stats/rating, idempotent match log.
-- Agent -> permission grants come from config/agents.json via cmd/bootstrap (same as every other game agent).

INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000046', 'deadweight.play',
   'Guest player token scope: play DEADWEIGHT and resume own stats. Nothing else.'),
  ('00000002-0000-4000-8000-000000000047', 'deadweight.bot.play',
   'DEADWEIGHT pool bots: authenticate to the game server and get a stable bot player identity'),
  ('00000002-0000-4000-8000-000000000048', 'deadweight.match.write',
   'DEADWEIGHT game server: report authoritative match results (updates player ratings)'),
  ('00000002-0000-4000-8000-000000000049', 'deadweight.checkpoints.write',
   'Upload DEADWEIGHT RL checkpoints to the game-scoped checkpoint registry');

INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000047'),
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000048'),
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000049');

INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-000000000018', '00000000-0000-4000-8000-000000000001',
   'DEADWEIGHT-BOTS', 'game_bot_agent', 'ACTIVE', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)),
  ('00000003-0000-4000-8000-000000000019', '00000000-0000-4000-8000-000000000001',
   'DEADWEIGHT-SERVER', 'game_bot_agent', 'ACTIVE', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)),
  ('00000003-0000-4000-8000-00000000001a', '00000000-0000-4000-8000-000000000001',
   'DEADWEIGHT-RL', 'game_bot_agent', 'ACTIVE', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));

-- sha256(secret) only; the secret is 256 random bits so a fast hash is sufficient and the hot login path stays cheap.
CREATE TABLE IF NOT EXISTS game_guest_credentials (
    player_id   CHAR(36)    NOT NULL PRIMARY KEY,
    game        VARCHAR(32) NOT NULL,
    secret_hash CHAR(64)    NOT NULL,
    created_at  DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS game_player_stats (
    player_id     CHAR(36)    NOT NULL,
    game          VARCHAR(32) NOT NULL,
    rating        REAL        NOT NULL DEFAULT 1500,
    wins          INTEGER     NOT NULL DEFAULT 0,
    losses        INTEGER     NOT NULL DEFAULT 0,
    draws         INTEGER     NOT NULL DEFAULT 0,
    matches       INTEGER     NOT NULL DEFAULT 0,
    last_match_at DATETIME,
    PRIMARY KEY (player_id, game)
);
CREATE INDEX IF NOT EXISTS idx_game_player_stats_rating ON game_player_stats(game, rating);

CREATE TABLE IF NOT EXISTS game_matches (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    game             VARCHAR(32) NOT NULL,
    match_id         INTEGER     NOT NULL,
    seed             INTEGER     NOT NULL,
    seat0_player_id  CHAR(36)    NOT NULL,
    seat1_player_id  CHAR(36)    NOT NULL,
    winner           INTEGER     NOT NULL,
    rounds           INTEGER     NOT NULL,
    reason           VARCHAR(16) NOT NULL,
    mode             INTEGER     NOT NULL DEFAULT 0,
    reported_at      DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(game, match_id, seed, seat0_player_id, seat1_player_id)
);
