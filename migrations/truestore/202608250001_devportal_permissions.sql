-- Gates the new notebook portal (okemily.com/portal -- links out to
-- Jupyter and, eventually, SARENA_NOTEBOOK) behind a real, dedicated
-- permission rather than reusing iduna.admin: this is a developer
-- tool, not IDUNA administration, and the two shouldn't be coupled.
--
-- Deliberately NOT granted to any role here, including super_admin --
-- founder, real-time, repeated: "it can be public at first but not for
-- very long" / "if you dont [figure out] the security on the python
-- notbook wont work figure it out." A brand-new permission with zero
-- default holders means the portal is functionally inaccessible to
-- everyone, including the founder's own human account, until it is
-- explicitly granted via the existing /admin/users role-assignment UI
-- (admin.go's userAction "roles" case) -- the same real, working RBAC
-- grant flow every other permission in this table already goes
-- through, not a new one-off mechanism.
INSERT OR IGNORE INTO permissions(id, name, description) VALUES
    ('00000002-0000-4000-8000-000000000040', 'devportal.access', 'Access the developer notebook portal (Jupyter/SARENA_NOTEBOOK)');

INSERT OR IGNORE INTO roles (id, name, description) VALUES
    ('00000001-0000-4000-8000-000000000006', 'devportal', 'Developer notebook portal access (Jupyter/SARENA_NOTEBOOK)');

INSERT OR IGNORE INTO role_permissions (role_id, permission_id) VALUES
    ('00000001-0000-4000-8000-000000000006', '00000002-0000-4000-8000-000000000040');
