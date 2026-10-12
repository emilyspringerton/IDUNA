-- WOTAN Phase 2 Clubs (WOTAN/CLUBS_NORTHSTAR.md section 5): clubs (teams), memberships with roles, invites,
-- join requests, a members-only strategy feed, featured decks, an append-only audit log, telemetry sync
-- targets, self-declared player rank, plus platform-level settings and the wotan.club* permission catalog.
-- Roles are enforced server-side from club_memberships on every request (never from the JWT).

CREATE TABLE IF NOT EXISTS clubs (
    id               TEXT PRIMARY KEY,
    slug             TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name             TEXT NOT NULL,
    tagline          TEXT NOT NULL DEFAULT '',
    description      TEXT NOT NULL DEFAULT '',
    privacy_tier     INTEGER NOT NULL DEFAULT 1,          -- 1 secret, 2 private, 3 social
    prev_tier        INTEGER,                              -- remembered tier for the one-click Reveal
    is_exclusive     INTEGER NOT NULL DEFAULT 0,
    join_policy      TEXT NOT NULL DEFAULT 'invite',       -- invite | request (request: social only)
    show_contributors INTEGER NOT NULL DEFAULT 0,
    owner_sub        TEXT NOT NULL DEFAULT '',
    brand_json       TEXT NOT NULL DEFAULT '{}',           -- {accent, banner_text}; no image uploads in v0
    suspended        INTEGER NOT NULL DEFAULT 0,
    suspended_reason TEXT NOT NULL DEFAULT '',
    deleted          INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS club_memberships (
    club_id        TEXT NOT NULL,
    sub            TEXT NOT NULL,
    role           TEXT NOT NULL CHECK (role IN ('player','coach','manager')),
    public_visible INTEGER NOT NULL DEFAULT 1,
    show_badge     INTEGER NOT NULL DEFAULT 1,
    joined_at      TEXT NOT NULL DEFAULT (datetime('now')),
    invited_by     TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (club_id, sub)
);
CREATE INDEX IF NOT EXISTS idx_club_memberships_sub ON club_memberships (sub);

CREATE TABLE IF NOT EXISTS club_invites (
    id          TEXT PRIMARY KEY,
    club_id     TEXT NOT NULL,
    token_hash  TEXT NOT NULL UNIQUE,                      -- sha256 only; the plaintext is shown once
    role        TEXT NOT NULL DEFAULT 'player',
    created_by  TEXT NOT NULL,
    bound_sub   TEXT NOT NULL DEFAULT '',
    expires_at  TEXT NOT NULL,
    used_by     TEXT NOT NULL DEFAULT '',
    used_at     TEXT,
    revoked_at  TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_club_invites_club ON club_invites (club_id);

CREATE TABLE IF NOT EXISTS club_join_requests (
    id         TEXT PRIMARY KEY,
    club_id    TEXT NOT NULL,
    sub        TEXT NOT NULL,
    message    TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'pending',            -- pending | accepted | rejected | cancelled
    decided_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    decided_at TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_club_join_requests_pending ON club_join_requests (club_id, sub) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS club_posts (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    club_id        TEXT NOT NULL,
    author_sub     TEXT NOT NULL,
    body           TEXT NOT NULL,
    pinned         INTEGER NOT NULL DEFAULT 0,
    pinned_deck_id INTEGER NOT NULL DEFAULT 0,
    deleted        INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_club_posts_club ON club_posts (club_id, id DESC);

CREATE TABLE IF NOT EXISTS club_post_comments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    post_id    INTEGER NOT NULL,
    club_id    TEXT NOT NULL,
    author_sub TEXT NOT NULL,
    body       TEXT NOT NULL,
    deleted    INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_club_post_comments_post ON club_post_comments (post_id, id);

CREATE TABLE IF NOT EXISTS club_featured_decks (
    club_id     TEXT NOT NULL,
    deck_id     INTEGER NOT NULL,
    featured_by TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (club_id, deck_id)
);

CREATE TABLE IF NOT EXISTS club_audit_log (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    club_id        TEXT NOT NULL,
    actor_sub      TEXT NOT NULL DEFAULT '',
    action         TEXT NOT NULL,
    target_sub     TEXT NOT NULL DEFAULT '',
    meta_json      TEXT NOT NULL DEFAULT '{}',
    platform_admin INTEGER NOT NULL DEFAULT 0,
    at             TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_club_audit_club ON club_audit_log (club_id, id DESC);

-- Telemetry routing preference (section 6; the endpoints that use these two tables are chunk P2-B).
CREATE TABLE IF NOT EXISTS hs_sync_targets (
    sub        TEXT PRIMARY KEY,
    kind       TEXT NOT NULL DEFAULT 'personal' CHECK (kind IN ('personal','club','none')),
    club_id    TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS hs_player_rank (
    sub               TEXT PRIMARY KEY,
    battletag         TEXT NOT NULL DEFAULT '',
    battletag_verified INTEGER NOT NULL DEFAULT 0,
    region            TEXT NOT NULL DEFAULT '',
    legend_rank       INTEGER NOT NULL DEFAULT 0,
    rank_verified     INTEGER NOT NULL DEFAULT 0,
    source            TEXT NOT NULL DEFAULT 'self',
    updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
);

ALTER TABLE hs_games ADD COLUMN club_id TEXT NOT NULL DEFAULT '';
ALTER TABLE hs_games ADD COLUMN visibility TEXT NOT NULL DEFAULT 'personal'; -- personal | club | private
CREATE INDEX IF NOT EXISTS idx_hs_games_club ON hs_games (club_id, id DESC);

-- Platform-level caps edited from the Back Office; defaults live in code (internal/http/handlers/clubs.go).
-- Recognized keys: clubs_per_user, members_per_club, invite_ttl_max_hours, pending_invites_per_club.
CREATE TABLE IF NOT EXISTS club_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);

-- Permission catalog (same convention as 202609050002_mailinglist_admin_permission.sql).
-- wotan.club.create is registered for the catalog view but deliberately granted to no role: player accounts
-- carry no role-based permissions, so creation is implicit for any authenticated subject that owns a handle.
INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('00000002-0000-4000-8000-00000000005c', 'wotan.club.create', 'Create a WOTAN club (implicit for any signed-in player with a handle; capped per user)'),
    ('00000002-0000-4000-8000-00000000005d', 'wotan.clubs.admin', 'Platform administration of WOTAN clubs: suspend, transfer, delete, audit (/api/v1/clubs/admin/*)'),
    ('00000002-0000-4000-8000-00000000005e', 'wotan.clubs.moderate', 'Read-only platform moderation view of WOTAN clubs (/api/v1/clubs/admin/*)');

INSERT OR IGNORE INTO role_permissions (role_id, permission_id) VALUES
    ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-00000000005d'),
    ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-00000000005e');
