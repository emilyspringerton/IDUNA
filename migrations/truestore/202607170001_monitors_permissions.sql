-- Backfill: monitors.read/create/alert permissions were referenced in
-- config/agents.json (EMILY-PRIME) by the S131 check-in monitors work
-- (202606250003_monitors.sql) but never seeded into the permissions table —
-- cmd/bootstrap has failed on every fresh setup since, with "permission
-- monitors.read not found in DB". Found while registering a new agent
-- (NORN, S141-04) and running bootstrap for the first time since S131.
--
-- Also found while writing this: the role_permissions grant pattern used by
-- 202606090002_camera_observations.sql (WHERE r.name IN ('emily_prime',
-- 'emily_agent', 'agent_default')) has always been a silent no-op — none of
-- those role names exist anywhere (202602220002_iam_rbac.sql only seeds
-- super_admin/admin/operator/analyst/agent_owner). Not fixed here (out of
-- scope for this migration — flagged in EMILY/BACKLOG.md instead); this
-- migration uses the real role names so it doesn't repeat that mistake.

INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('00000002-0000-4000-8000-000000000026', 'monitors.read',   'Read check-in monitor status'),
    ('00000002-0000-4000-8000-000000000027', 'monitors.create', 'Create check-in monitors'),
    ('00000002-0000-4000-8000-000000000028', 'monitors.alert',  'Receive/acknowledge check-in monitor alerts');

INSERT OR IGNORE INTO role_permissions(role_id, permission_id)
SELECT r.id, p.id
FROM roles r, permissions p
WHERE r.name IN ('super_admin', 'admin', 'operator')
  AND p.name IN ('monitors.read', 'monitors.create', 'monitors.alert');
