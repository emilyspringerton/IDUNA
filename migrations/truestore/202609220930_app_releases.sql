-- App release registry (S528, founder real-time: "we need an app repository in IDUNA signed in
-- github somehow with emily session new for the build before each build and we can use session
-- tokens in the app binaries figure out how to sign them with gpg keys or something").
--
-- Real, shared registry for the bundled SHANKPIT-launchable app binaries (SHANKPIT/apps/lobby's
-- new "Apps" page, S527 -- DEADWEIGHT first). Mirrors shankpit_rl_checkpoints' own real shape
-- (202609150022_shankpit_rl_checkpoints.sql) field-for-field: SQLite metadata + on-disk blob,
-- same reason -- a CI runner needs a real, network-reachable place to publish a signed binary
-- that SHANKPIT's lobby (or a future real download-on-demand path) can fetch from, not a local
-- directory only this box can see.
--
-- Real, new fields beyond the checkpoint precedent: session_tag (this session's own `emily
-- session new` fingerprint format, reused here as a lightweight per-build provenance nonce, NOT
-- an LLM-conversation session -- see internal/apps/release_store.go's own doc comment),
-- gpg_signature (armored detached signature, small enough to store inline as TEXT rather than a
-- second blob), gpg_key_id (which key signed it, for future key rotation), platform (one binary
-- per platform per version), github_commit_sha/github_run_url (real CI provenance -- "signed in
-- github somehow" is satisfied by recording exactly which GitHub Actions run produced this
-- artifact, not a separate GitHub-side signing mechanism).
CREATE TABLE IF NOT EXISTS app_releases (
    id               INTEGER  PRIMARY KEY AUTOINCREMENT,
    app_slug         VARCHAR(32)  NOT NULL,
    platform         VARCHAR(32)  NOT NULL,
    version          VARCHAR(64)  NOT NULL,
    session_tag      VARCHAR(64)  NOT NULL,
    filename         VARCHAR(200) NOT NULL,
    sha256           VARCHAR(64)  NOT NULL,
    size_bytes       INTEGER  NOT NULL,
    blob_path        VARCHAR(500) NOT NULL,
    gpg_signature    TEXT     NOT NULL DEFAULT '',
    gpg_key_id       VARCHAR(64)  NOT NULL DEFAULT '',
    github_commit_sha VARCHAR(64) NOT NULL DEFAULT '',
    github_run_url   VARCHAR(300) NOT NULL DEFAULT '',
    is_latest        INTEGER  NOT NULL DEFAULT 0,
    created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_app_releases_slug_platform ON app_releases(app_slug, platform);

-- Real, narrow, least-privilege permission -- mirrors shankpit.checkpoints.write's own exact
-- shape. Only a CI upload step needs this; nothing else.
INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000052',
   'apps.releases.write',
   'Upload a signed app-release binary + metadata to the shared app-release registry');

INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000052');

-- The real agent row itself -- config/agents.json only feeds cmd/bootstrap's own permission-
-- grant/secret-provisioning steps; the agent row has to actually exist first, same real pattern
-- 202609181810_deadweight_agents_and_stats.sql already established (missed on the first pass of
-- this migration, found live when `go run ./cmd/bootstrap` reported "not found in agents table").
INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-00000000001b', '00000000-0000-4000-8000-000000000001',
   'APP-RELEASES-CI', 'ci_agent', 'ACTIVE', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
