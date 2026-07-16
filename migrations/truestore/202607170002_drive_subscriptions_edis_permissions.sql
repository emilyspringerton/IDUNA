-- Backfill: three more permissions referenced in config/agents.json but
-- never seeded into the permissions table, found the same way as
-- 202607170001 (running cmd/bootstrap fresh while registering the NORN
-- agent for S141-04 — apparently nobody has run bootstrap clean since
-- these features landed):
--   drive.read / drive.write     — EMILY-TRAINING (S26-01 IDUNA Drive API)
--   edis.secrets.read            — EDIS-CUSTODIAN (S23-06 DIS provisioning)
--   subscriptions.admin          — EDIS-WOOCOMMERCE (S23-04 Emily+ subscriptions)
--
-- No role_permissions grants here — unlike monitors.* (202607170001), these
-- don't have an obvious existing role to attach to among
-- super_admin/admin/operator/analyst/agent_owner, and guessing one would be
-- an RBAC policy decision, not a bootstrap-unblocking bug fix. Agent-level
-- grants (config/agents.json) don't go through roles, so this alone is
-- sufficient to unblock cmd/bootstrap.

INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('00000002-0000-4000-8000-000000000029', 'drive.read',          'Read/list files via the IDUNA Drive API (training artifacts)'),
    ('00000002-0000-4000-8000-000000000030', 'drive.write',         'Upload files via the IDUNA Drive API (training artifacts)'),
    ('00000002-0000-4000-8000-000000000031', 'edis.secrets.read',   'Read IDUNA-managed secrets for provisioning to an EDIS WordPress instance'),
    ('00000002-0000-4000-8000-000000000032', 'subscriptions.admin', 'Provision and manage Emily+ subscriptions');
