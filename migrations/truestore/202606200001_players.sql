-- S43-02: SHANKPIT player registry.
-- One row per player identity. provider + provider_sub form the unique identity key.
-- A player who re-registers via the same OAuth provider is upserted (login = register).
CREATE TABLE IF NOT EXISTS players (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    player_id    CHAR(36)     NOT NULL UNIQUE,  -- UUID v4, stable across sessions
    display_name VARCHAR(64)  NOT NULL,
    provider     VARCHAR(32)  NOT NULL,          -- "google", "iduna_local", "anonymous"
    provider_sub VARCHAR(256) NOT NULL,          -- OAuth subject or local user UID
    email        VARCHAR(256),
    kills        INTEGER      NOT NULL DEFAULT 0,
    deaths       INTEGER      NOT NULL DEFAULT 0,
    sessions     INTEGER      NOT NULL DEFAULT 0,
    registered_at DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_seen    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(provider, provider_sub)
);
CREATE INDEX IF NOT EXISTS idx_players_provider ON players(provider, provider_sub);
