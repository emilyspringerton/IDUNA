-- S43-05: Email + password credentials for SHANKPIT players who don't have Google.
-- Linked to players table via player_id.
CREATE TABLE IF NOT EXISTS player_credentials (
    player_id     CHAR(36)     NOT NULL PRIMARY KEY,
    email         VARCHAR(256) NOT NULL UNIQUE,
    password_hash VARCHAR(256) NOT NULL,  -- bcrypt
    created_at    DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (player_id) REFERENCES players(player_id)
);
