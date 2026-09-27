-- NOCK video editor (founder real-time, 2026-09-27: "blue ocean we need a nock video editor that
-- can take uploads from any phone via nock"). See internal/nock/video_store.go's header comment.
--
-- Unlike nock_textures/nock_animations (BLOB-in-SQLite), video bytes live on disk under
-- NOCK_VIDEO_DIR; these rows hold paths RELATIVE to that dir, so the dir can move.

CREATE TABLE IF NOT EXISTS nock_video_upload_links (
    id            INTEGER  PRIMARY KEY AUTOINCREMENT,
    token         VARCHAR(64)  NOT NULL,     -- capability token in the phone URL
    label         VARCHAR(200) NOT NULL DEFAULT '',
    created_by    VARCHAR(200),
    expires_at    VARCHAR(40)  NOT NULL,     -- RFC3339 UTC; compared lexically
    max_uploads   INTEGER  NOT NULL DEFAULT 0, -- 0 = unlimited until expiry
    upload_count  INTEGER  NOT NULL DEFAULT 0,
    revoked       INTEGER  NOT NULL DEFAULT 0,
    created_at    VARCHAR(40)  NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nock_video_upload_links_token ON nock_video_upload_links(token);

CREATE TABLE IF NOT EXISTS nock_videos (
    id                INTEGER  PRIMARY KEY AUTOINCREMENT,
    name              VARCHAR(200) NOT NULL,
    original_filename VARCHAR(255) NOT NULL DEFAULT '',
    file_path         VARCHAR(255) NOT NULL,
    size_bytes        INTEGER  NOT NULL,
    duration_ms       INTEGER  NOT NULL DEFAULT 0,
    width             INTEGER  NOT NULL DEFAULT 0,
    height            INTEGER  NOT NULL DEFAULT 0,
    video_codec       VARCHAR(40)  NOT NULL DEFAULT '',
    has_audio         INTEGER  NOT NULL DEFAULT 0,
    fps               REAL     NOT NULL DEFAULT 0,
    proxy_status      VARCHAR(20)  NOT NULL DEFAULT 'pending', -- pending | ready | failed
    proxy_error       TEXT,
    proxy_path        VARCHAR(255),
    thumb_path        VARCHAR(255),
    source            VARCHAR(20)  NOT NULL DEFAULT 'admin',   -- admin | phone
    upload_link_id    INTEGER,
    uploaded_by       VARCHAR(200),
    created_at        VARCHAR(40)  NOT NULL
);

CREATE TABLE IF NOT EXISTS nock_video_timelines (
    id             INTEGER  PRIMARY KEY AUTOINCREMENT,
    name           VARCHAR(200) NOT NULL,
    edl_json       TEXT     NOT NULL,           -- {"width","height","fps","segments":[{clip_id,in_ms,out_ms}]}
    render_status  VARCHAR(20)  NOT NULL DEFAULT 'none', -- none | rendering | ready | failed | stale
    render_error   TEXT,
    render_path    VARCHAR(255),
    rendered_at    VARCHAR(40),
    created_at     VARCHAR(40)  NOT NULL,
    updated_at     VARCHAR(40)  NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nock_video_timelines_name ON nock_video_timelines(name);

-- Reuses iduna.admin for every /admin/nock route; the phone upload page is gated by the link
-- token itself, not a permission.
