-- System seeds: agent-scoped permissions, system owner user, and system agent stubs.
-- All INSERTs use IGNORE so this migration is fully idempotent and safe to re-run.
-- The bootstrap command reads config/agents.json to provision credentials after this runs.

-- ── New permissions (agent-scoped) ──────────────────────────────────────────
-- IDs continue from 000000000012 (apples migration).
-- Agents get direct permission grants from agent_permissions, NOT via roles.

INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000013', 'fatbaby.operator',     'Operate the fatbaby pipeline: start/stop processes, read logs, write observations'),
  ('00000002-0000-4000-8000-000000000014', 'emily-prime.operator', 'Operate Emily Prime: read/write directives and tasks in the integration layer'),
  ('00000002-0000-4000-8000-000000000015', 'emiree.super',         'Emiree top-level authority: peer loop with Emily Prime, strategic state'),
  ('00000002-0000-4000-8000-000000000016', 'bob.db.admin',         'Bob database administration: migrations, schema inspection, read-only queries'),
  ('00000002-0000-4000-8000-000000000017', 'signalapi.read',       'Read-only access to the signal API and entity graph outputs'),
  ('00000002-0000-4000-8000-000000000018', 'jon.setups.write',     'Log and publish trade setups from Jon Stockwell agent');

-- super_admin inherits all new permissions.
INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000013'),
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000014'),
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000015'),
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000016'),
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000017'),
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000018');

-- ── System owner user ────────────────────────────────────────────────────────
-- Agents require an owner_user_id FK. This SYSTEM user owns all system agents.
-- google_subject uses a synthetic value that will never match a real Google account.
-- Status ACTIVE so FK constraints are satisfied. Never used for human login.

INSERT IGNORE INTO users (id, email, google_subject, status, honor_accepted_current, honor_code_sha, honor_code_version, created_at, updated_at) VALUES
  ('00000000-0000-4000-8000-000000000001',
   'system@einhorn.internal',
   'system:einhorn-industrial:non-human',
   'ACTIVE',
   1,
   'system',
   0,
   CURRENT_TIMESTAMP(6),
   CURRENT_TIMESTAMP(6));

-- ── System agent stubs ───────────────────────────────────────────────────────
-- These rows are created with NULL api_key_hash. The bootstrap command generates
-- a random secret per agent, stores the hash, and writes the plaintext to
-- var/agent-secrets.env. The IDs are deterministic so re-running is safe.
--
-- Agents do NOT get permissions here — agent_permissions are set by the bootstrap
-- command reading config/agents.json, keeping permissions as config-as-code.

INSERT IGNORE INTO agents (id, owner_user_id, name, type, status, created_at, updated_at) VALUES
  ('00000003-0000-4000-8000-000000000001',
   '00000000-0000-4000-8000-000000000001',
   'EMILY-PRIME', 'llm_agent', 'ACTIVE',
   CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)),

  ('00000003-0000-4000-8000-000000000002',
   '00000000-0000-4000-8000-000000000001',
   'FATBABY-EMILY', 'llm_agent', 'ACTIVE',
   CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)),

  ('00000003-0000-4000-8000-000000000003',
   '00000000-0000-4000-8000-000000000001',
   'EMIREE', 'llm_agent', 'ACTIVE',
   CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)),

  ('00000003-0000-4000-8000-000000000004',
   '00000000-0000-4000-8000-000000000001',
   'JON', 'llm_agent', 'ACTIVE',
   CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6)),

  ('00000003-0000-4000-8000-000000000005',
   '00000000-0000-4000-8000-000000000001',
   'BOB', 'db_agent', 'ACTIVE',
   CURRENT_TIMESTAMP(6), CURRENT_TIMESTAMP(6));
