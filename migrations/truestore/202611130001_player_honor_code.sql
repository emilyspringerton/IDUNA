-- THE_HONOR_CODE sealed by a generic IDUNA SSO identity (a players row): the WOTAN Valhöll oath page,
-- POST /api/v1/honor-code/accept (internal/http/handlers/honor_code_api.go). The web ceremony Google identities
-- keep their acceptance on the users row (honor_* columns, 202602220002). The sub of an SSO JWT is a
-- players.player_id, which that UPDATE never matched. One row per (player, version): the first seal of a version
-- stands and is never rewritten or deleted by the API, and sealing again is an INSERT OR IGNORE. accepted_at is
-- RFC 3339 UTC, written by the handler.
CREATE TABLE IF NOT EXISTS player_honor_code (
    player_id   CHAR(36) NOT NULL,
    version     INTEGER  NOT NULL,
    sha256      CHAR(64) NOT NULL,
    accepted_at TEXT     NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (player_id, version)
);
