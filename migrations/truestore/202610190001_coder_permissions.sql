-- coder.* permissions for the coder agent API (internal/coder, internal/http/handlers/coder.go).
-- IDUNA refuses to grant a permission that is not registered here ('permission ... not found'),
-- found live when minting the first worker agent. coder.tool.<name> is the per-tool grant that
-- IDUNA checks against the sender of a turn when the worker reports a tool call. coder.admin and
-- iduna.admin are supersets in code (internal/coder/perms.go), not via role rows.
INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('00000002-0000-4000-8000-000000000100', 'coder.admin', 'Superset: every coder.* API and tool'),
    ('00000002-0000-4000-8000-000000000101', 'coder.session.create', 'Create coder-agent chat sessions'),
    ('00000002-0000-4000-8000-000000000102', 'coder.session.read', 'Read your own coder-agent sessions and messages'),
    ('00000002-0000-4000-8000-000000000103', 'coder.session.read.all', 'Read any user coder-agent sessions and messages'),
    ('00000002-0000-4000-8000-000000000104', 'coder.chat.send', 'Send messages to the coder agent (in your own sessions)'),
    ('00000002-0000-4000-8000-000000000105', 'coder.tools.read', 'List the coder agent tools'),
    ('00000002-0000-4000-8000-000000000106', 'coder.lessons.read', 'Read the coder agent lessons'),
    ('00000002-0000-4000-8000-000000000107', 'coder.lessons.write', 'Add or remove the coder agent lessons (the feedback loop)'),
    ('00000002-0000-4000-8000-000000000108', 'coder.worker.claim', 'Worker: claim queued coder-agent turns'),
    ('00000002-0000-4000-8000-000000000109', 'coder.worker.report', 'Worker: report tool calls/replies for a claimed turn'),
    ('00000002-0000-4000-8000-00000000010a', 'coder.worker.provision', 'Mint a least-privilege coder worker agent'),
    ('00000002-0000-4000-8000-00000000010b', 'coder.tool.observe', 'Let the coder agent use its observe tool on your behalf'),
    ('00000002-0000-4000-8000-00000000010c', 'coder.tool.apples', 'Let the coder agent use its apples tool on your behalf'),
    ('00000002-0000-4000-8000-00000000010d', 'coder.tool.changelog', 'Let the coder agent use its changelog tool on your behalf'),
    ('00000002-0000-4000-8000-00000000010e', 'coder.tool.backlog', 'Let the coder agent use its backlog tool on your behalf'),
    ('00000002-0000-4000-8000-00000000010f', 'coder.tool.status', 'Let the coder agent use its status tool on your behalf'),
    ('00000002-0000-4000-8000-000000000110', 'coder.tool.grep', 'Let the coder agent use its grep tool on your behalf'),
    ('00000002-0000-4000-8000-000000000111', 'coder.tool.sed', 'Let the coder agent use its sed tool on your behalf'),
    ('00000002-0000-4000-8000-000000000112', 'coder.tool.go', 'Let the coder agent use its go tool on your behalf'),
    ('00000002-0000-4000-8000-000000000113', 'coder.tool.git', 'Let the coder agent use its git tool on your behalf'),
    ('00000002-0000-4000-8000-000000000114', 'coder.tool.files', 'Let the coder agent use its files tool on your behalf'),
    ('00000002-0000-4000-8000-000000000115', 'coder.tool.claude_files', 'Let the coder agent use its claude_files tool on your behalf'),
    ('00000002-0000-4000-8000-000000000116', 'coder.tool.finish', 'Let the coder agent use its finish tool on your behalf');
