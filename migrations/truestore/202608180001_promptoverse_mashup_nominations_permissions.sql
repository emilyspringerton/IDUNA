-- Mashup nomination review permission. "build out mashup nomination as a
-- social tool" -- a logged-in, honor-code-accepted user can nominate
-- combining two existing subjects into a new mashup subject
-- (POST /api/v1/promptoverse/mashup-nominations, open to any authenticated
-- user, no special permission needed). Reviewing (approve/reject) a
-- nomination is admin-only, gated on this permission -- matches the
-- founder's own rule that promotion approvals run through
-- EINHORN_INDUSTRIAL admins before hitting the real gen pipeline.

INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('00000002-0000-4000-8000-000000000040', 'promptoverse.mashups.review', 'Approve or reject Prompt-o-verse mashup nominations');

INSERT OR IGNORE INTO role_permissions (role_id, permission_id) VALUES
    ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000040');
