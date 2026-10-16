-- A guest game account upgraded in place with an IDUNA SSO identity ("Save progress with IDUNA").
-- The guest keeps its own player_id (tickets, rings, friends, history); this maps the SSO
-- identity's player_id onto it so sso-exchange resolves to the guest's row.
CREATE TABLE IF NOT EXISTS game_sso_links (
    sso_player_id CHAR(36)    NOT NULL PRIMARY KEY,
    player_id     CHAR(36)    NOT NULL,
    game          VARCHAR(32) NOT NULL,
    created_at    DATETIME    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
