-- K8s migration VS0 (EMILY/docs/KUBERNETES_SERVICE_MIGRATION_NORTHSTAR.md): emily-agent's
-- collections-server (golden docs first) validates IDUNA ES256 JWTs and requires this
-- permission. Read-only by design: no write counterpart exists. Grants come from
-- config/agents.json via cmd/bootstrap (same as every other agent). Mirrors the D2 shape
-- (202609251200_rename_deadweight2_to_d2.sql).
INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-00000000005a', 'emily.collections.read',
   'Read-only access to emily-agent collections API (golden docs, later var/ read models). No write counterpart.');

INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-00000000005a');

INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-00000000001f', '00000000-0000-4000-8000-000000000001',
   'EMILY-COLLECTIONS-READER', 'llm_agent', 'ACTIVE', CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
