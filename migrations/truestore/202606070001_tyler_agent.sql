-- tyler-agent: TYLER RSI loop agent registration.
-- Iduna-managed, self-directed, git-authoritative.
-- Custody chain: TYLER → EINHORN_INDUSTRIAL → EMILY_OS
-- Ref: TYLER repo Build 0016 (_.md / __.md)

-- ── Permission ────────────────────────────────────────────────────────────────

INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000019',
   'tyler.rsi.write',
   'Tyler agent: write RSI artifacts (lore, episodes, engine specs, faction memos) to the TYLER repo');

-- super_admin inherits.
INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000019');

-- ── Agent stub ────────────────────────────────────────────────────────────────

INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-000000000006',
   '00000000-0000-4000-8000-000000000001',
   'TYLER', 'llm_agent', 'ACTIVE',
   CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
