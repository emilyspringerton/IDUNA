-- Real, found-live bug (S506, 2026-09-21): the OpenExecutive M2M provision API
-- (POST /api/v1/openexecutive/provision) and its own documented example
-- permissions ("openexec.read", "openexec.admin" -- IDUNA/CLAUDE.md's own
-- endpoint table) referenced these permission names since the endpoint was
-- first added, but no migration ever registered them as real rows -- so every
-- provision call granting either one has always failed closed
-- ("permission \"openexec.read\" not found"), silently, until the new Back
-- Office /admin/openexecutive page actually exercised the form end-to-end and
-- surfaced it. GrantAgentPermission requires the permission to pre-exist
-- (FK-style lookup by name), same as every other permission in this table.
INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('00000002-0000-4000-8000-000000000050', 'openexec.read', 'OpenExecutive: read-only access to its own IDUNA-backed identity/session endpoints'),
    ('00000002-0000-4000-8000-000000000051', 'openexec.admin', 'OpenExecutive: full admin access to its own IDUNA-backed identity/session endpoints');
