-- S75-01: DragonsNShit MMO schema.
-- Depends on: 202606200001_players.sql (players.player_id).
-- All character/item/guild/world state persisted here; IDUNA is the authoritative
-- store for the DragonsNShit MMO backend (GFD server-go).

-- ── Characters ──────────────────────────────────────────────────────────────
-- One row per in-game character owned by a player.
CREATE TABLE IF NOT EXISTS characters (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    character_id    CHAR(36) NOT NULL UNIQUE,   -- UUID v4
    player_id       CHAR(36) NOT NULL,           -- FK → players.player_id
    name            VARCHAR(32) NOT NULL UNIQUE,
    scene_id        INTEGER  NOT NULL DEFAULT 0,
    pos_x           REAL     NOT NULL DEFAULT 0.0,
    pos_y           REAL     NOT NULL DEFAULT 0.0,
    pos_z           REAL     NOT NULL DEFAULT 0.0,
    gold_balance    INTEGER  NOT NULL DEFAULT 0,
    level           INTEGER  NOT NULL DEFAULT 1,
    current_xp      INTEGER  NOT NULL DEFAULT 0,
    job_main        VARCHAR(4) NOT NULL DEFAULT 'WAR',
    job_sub         VARCHAR(4) NOT NULL DEFAULT '',
    home_scene_id   INTEGER  NOT NULL DEFAULT 0,
    home_pos_x      REAL     NOT NULL DEFAULT 0.0,
    home_pos_y      REAL     NOT NULL DEFAULT 0.0,
    home_pos_z      REAL     NOT NULL DEFAULT 0.0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (player_id) REFERENCES players(player_id)
);
CREATE INDEX IF NOT EXISTS idx_characters_player   ON characters(player_id);
CREATE INDEX IF NOT EXISTS idx_characters_scene    ON characters(scene_id);
CREATE INDEX IF NOT EXISTS idx_characters_name     ON characters(name);

-- ── Character Skills ─────────────────────────────────────────────────────────
-- One row per (character, skill) pair. skill_name is a canonical string
-- matching server/gather.SkillMin constants and server/craft CraftType.
CREATE TABLE IF NOT EXISTS character_skills (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    character_id    CHAR(36) NOT NULL,
    skill_name      VARCHAR(32) NOT NULL,
    value           REAL     NOT NULL DEFAULT 0.0,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(character_id, skill_name),
    FOREIGN KEY (character_id) REFERENCES characters(character_id)
);
CREATE INDEX IF NOT EXISTS idx_char_skills_char ON character_skills(character_id);

-- ── Items ────────────────────────────────────────────────────────────────────
-- Every item has a UUID and a full provenance chain as JSON.
-- provenance_chain is a JSON array of {"actor_id":..., "action":..., "at":...}.
-- destroyed_at is set on soft-delete (equipped items cannot be destroyed).
CREATE TABLE IF NOT EXISTS items (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    item_id         CHAR(36) NOT NULL UNIQUE,
    owner_character_id CHAR(36) NOT NULL,
    item_type       VARCHAR(64) NOT NULL,
    name            VARCHAR(128) NOT NULL,
    item_level      INTEGER  NOT NULL DEFAULT 0,
    quantity        INTEGER  NOT NULL DEFAULT 1,
    provenance_chain TEXT    NOT NULL DEFAULT '[]',  -- JSON array
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    destroyed_at    DATETIME,
    FOREIGN KEY (owner_character_id) REFERENCES characters(character_id)
);
CREATE INDEX IF NOT EXISTS idx_items_owner     ON items(owner_character_id);
CREATE INDEX IF NOT EXISTS idx_items_type      ON items(item_type);
CREATE INDEX IF NOT EXISTS idx_items_destroyed ON items(destroyed_at);

-- ── Guilds ───────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS guilds (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    guild_id    CHAR(36) NOT NULL UNIQUE,
    name        VARCHAR(64) NOT NULL UNIQUE,
    tag         VARCHAR(6)  NOT NULL UNIQUE,    -- short linkshell tag
    founder_id  CHAR(36)    NOT NULL,            -- characters.character_id
    disbanded_at DATETIME,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (founder_id) REFERENCES characters(character_id)
);

-- ── Guild Memberships ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS guild_memberships (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    guild_id        CHAR(36) NOT NULL,
    character_id    CHAR(36) NOT NULL,
    role            VARCHAR(16) NOT NULL DEFAULT 'member',  -- 'leader','officer','member'
    joined_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    left_at         DATETIME,
    UNIQUE(guild_id, character_id),
    FOREIGN KEY (guild_id)     REFERENCES guilds(guild_id),
    FOREIGN KEY (character_id) REFERENCES characters(character_id)
);
CREATE INDEX IF NOT EXISTS idx_guild_members_guild ON guild_memberships(guild_id);
CREATE INDEX IF NOT EXISTS idx_guild_members_char  ON guild_memberships(character_id);

-- ── World Events ─────────────────────────────────────────────────────────────
-- Tracks World Crisis instances and other global events.
CREATE TABLE IF NOT EXISTS world_events (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    event_id        CHAR(36) NOT NULL UNIQUE,
    event_type      VARCHAR(32) NOT NULL,     -- 'world_crisis', 'conquest_tick', etc.
    scene_id        INTEGER  NOT NULL DEFAULT 0,
    phase           VARCHAR(32) NOT NULL DEFAULT 'opening',
    ley_integrity   INTEGER  NOT NULL DEFAULT 100,
    started_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    resolved_at     DATETIME,
    outcome         TEXT                    -- JSON or plain text
);
CREATE INDEX IF NOT EXISTS idx_world_events_type  ON world_events(event_type);
CREATE INDEX IF NOT EXISTS idx_world_events_scene ON world_events(scene_id);

-- ── Scene State ───────────────────────────────────────────────────────────────
-- One row per scene; tracks ley integrity and active phase for World Crisis.
CREATE TABLE IF NOT EXISTS scene_state (
    scene_id        INTEGER  PRIMARY KEY,
    ley_integrity   INTEGER  NOT NULL DEFAULT 100,
    active_phase    VARCHAR(32) NOT NULL DEFAULT 'none',
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Seed default scenes (Meadow/Hills/Caves/Swampville).
INSERT OR IGNORE INTO scene_state (scene_id, ley_integrity, active_phase) VALUES
    (0, 100, 'none'),
    (1, 100, 'none'),
    (2, 100, 'none'),
    (3, 100, 'none');
