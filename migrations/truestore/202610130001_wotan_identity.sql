-- WOTAN Identity Federation, chunk ID-A (WOTAN/IDENTITY_NORTHSTAR.md section 3): linked accounts
-- (Battle.net / Google / Twitch / local), hashed tracker upload keys, per-player display prefs, the
-- Battle.net gate grace window, and an append-only identity audit log. Additive only. SQLite dialect,
-- same rules as 202610120001_wotan_clubs.sql. player_id is the master id (it equals the JWT sub).

CREATE TABLE IF NOT EXISTS player_identities (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    player_id        TEXT NOT NULL,
    provider         TEXT NOT NULL CHECK (provider IN ('battlenet','google','twitch','iduna_local')),
    provider_sub     TEXT NOT NULL,
    display          TEXT NOT NULL DEFAULT '',           -- battletag / twitch login / google email (never public by default)
    extra_json       TEXT NOT NULL DEFAULT '{}',         -- region hint, twitch broadcaster_type, ...
    verified         INTEGER NOT NULL DEFAULT 0,
    linked_at        TEXT NOT NULL DEFAULT (datetime('now')),
    last_login_at    TEXT,
    last_refreshed_at TEXT,
    status           TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','revoked')),
    UNIQUE (provider, provider_sub)
);
CREATE INDEX IF NOT EXISTS idx_player_identities_player ON player_identities (player_id, status);

-- Backfill: every existing google / iduna_local player gets an identity row so login resolution is uniform.
INSERT OR IGNORE INTO player_identities (player_id, provider, provider_sub, display, verified, linked_at, last_login_at, status)
SELECT player_id, provider, provider_sub, COALESCE(NULLIF(email,''), display_name),
       CASE provider WHEN 'google' THEN 1 ELSE 0 END, registered_at, last_seen, 'active'
  FROM players WHERE provider IN ('google','iduna_local');

CREATE TABLE IF NOT EXISTS hs_upload_keys (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    player_id             TEXT NOT NULL,
    key_prefix            TEXT NOT NULL,                  -- first 9 chars ("wt_ab12cd3") for the UI
    key_hash              TEXT NOT NULL UNIQUE,           -- sha256(key) hex; the key itself is shown once
    label                 TEXT NOT NULL DEFAULT '',
    created_at            TEXT NOT NULL DEFAULT (datetime('now')),
    last_used_at          TEXT,
    revoked_at            TEXT,
    uploads_ok            INTEGER NOT NULL DEFAULT 0,
    rejected_tag_mismatch INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_hs_upload_keys_player ON hs_upload_keys (player_id);

-- Per-player display prefs live next to the existing per-player hs_settings row (keyed by sub).
ALTER TABLE hs_settings ADD COLUMN show_twitch_name INTEGER NOT NULL DEFAULT 0;
ALTER TABLE hs_settings ADD COLUMN show_battletag INTEGER NOT NULL DEFAULT 0;

ALTER TABLE hs_games ADD COLUMN battletag_id INTEGER;                        -- player_identities.id the game was attributed to
ALTER TABLE hs_games ADD COLUMN first_streamed_live INTEGER NOT NULL DEFAULT 0;
ALTER TABLE hs_games ADD COLUMN unverified_tag INTEGER NOT NULL DEFAULT 0;   -- 1: generic/absent PlayerName, forced private, never routable

-- Gate grace window (section 5): players who already created clubs or have tracked games keep the
-- Battle.net-gated actions for 30 days. Absence of a row means no grace.
CREATE TABLE IF NOT EXISTS player_identity_grace (
    player_id   TEXT PRIMARY KEY,
    grace_until TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT ''
);
INSERT OR IGNORE INTO player_identity_grace (player_id, grace_until, reason)
SELECT owner_sub, datetime('now','+30 days'), 'grandfathered: owns a club' FROM clubs WHERE deleted=0 AND owner_sub<>'';
INSERT OR IGNORE INTO player_identity_grace (player_id, grace_until, reason)
SELECT DISTINCT owner_sub, datetime('now','+30 days'), 'grandfathered: tracked games' FROM hs_games WHERE owner_sub<>'';

-- Append-only audit of every identity link/unlink/key/merge action (operators and users alike).
CREATE TABLE IF NOT EXISTS identity_audit_log (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    player_id      TEXT NOT NULL DEFAULT '',             -- the player the action concerns
    actor_sub      TEXT NOT NULL DEFAULT '',
    action         TEXT NOT NULL,
    meta_json      TEXT NOT NULL DEFAULT '{}',
    platform_admin INTEGER NOT NULL DEFAULT 0,
    at             TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_identity_audit_player ON identity_audit_log (player_id, id DESC);
CREATE INDEX IF NOT EXISTS idx_identity_audit_action ON identity_audit_log (action, id DESC);

-- Permission catalog (same convention as the clubs migration). wotan.identity.link / unlink /
-- wotan.uploadkey.manage are implicit for any signed-in player. wotan.identity.bypass is granted per account
-- through player_permissions (Back Office Game Master tool) and skips the Battle.net gate.
INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('00000002-0000-4000-8000-00000000005f', 'wotan.identity.link', 'Link an additional login or streamer identity to your WOTAN player (implicit for any signed-in player)'),
    ('00000002-0000-4000-8000-000000000060', 'wotan.identity.unlink', 'Unlink one of your own identities, subject to the last-login-door guard (implicit for any signed-in player)'),
    ('00000002-0000-4000-8000-000000000061', 'wotan.uploadkey.manage', 'Create, list and revoke your own tracker upload keys (requires a linked Battle.net identity or an active grace window)'),
    ('00000002-0000-4000-8000-000000000062', 'wotan.identity.admin', 'Platform administration of WOTAN identities: force-unlink, revoke keys, merge players (Back Office /admin/identities)'),
    ('00000002-0000-4000-8000-000000000063', 'wotan.identity.bypass', 'Skip the Battle.net-link gate on club creation and upload-key minting (tournament organizers, test accounts)');

UPDATE permissions SET description = 'Create a WOTAN club (a signed-in player with a handle AND a linked Battle.net identity, or inside the 30-day grace window, or holding wotan.identity.bypass; capped per user)'
 WHERE name = 'wotan.club.create';

INSERT OR IGNORE INTO role_permissions (role_id, permission_id) VALUES
    ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000062');
