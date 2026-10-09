-- Real IAM for player/WOTAN accounts (founder real-time, 2026-10-09, S599-cont.: "we are going
-- to need an interface in iduna for adding roles to the email users"). player_email_auth.go's
-- own issueJWT never minted a "permissions" claim at all until the same-day fix that added
-- playerPermissions(email) -- a hardcoded allowlist, the same deliberate shape local_auth.go's
-- localUserPermissions already uses for the two local_users accounts. This table replaces that
-- allowlist with a real, admin-manageable grant: the Back Office Game Master tool
-- (admin_gm.go, already built to search players by email) gets a grant/revoke action per
-- account instead of a code change per account.
CREATE TABLE IF NOT EXISTS player_permissions (
    player_id  TEXT NOT NULL,
    permission TEXT NOT NULL,
    granted_by TEXT NOT NULL DEFAULT '',
    granted_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (player_id, permission)
);

-- Seed the one real grant that already existed as a hardcoded allowlist entry, so removing the
-- hardcoded version is a true no-op for the one account that actually has it.
INSERT IGNORE INTO player_permissions (player_id, permission, granted_by)
VALUES ('a3338fa1-062e-43e0-9493-b2e19bda7583', 'edge.game.operator', 'migration-202610090002');
