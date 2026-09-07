-- Real control-plane data model for IDUNA_PRO tenant provisioning (kanban follow-up to
-- IDUNA/docs/EMILY_FOR_BUSINESS_NORTHSTAR.md's own "control-plane model" section, founder
-- real-time 2026-09-03: "so we use our IDUNA to manage the free trials for emily for business").
--
-- Internal IDUNA stays the backbone and additionally tracks/provisions real, separate IDUNA_PRO
-- instances -- one row per tenant, each with its own SQLite file, port, and systemd unit,
-- reusing the SAME already-built idunapro binary (config-driven via env vars, no per-tenant
-- rebuild needed -- see internal/tenantprovision's own header comment for the full mechanism).
--
-- status lifecycle: "provisioning" (row created, systemd unit not up yet) -> "active" (real
-- health check passed) -> "failed" (provisioning errored, real error recorded) -> "expired"
-- (trial lapsed, not yet enforced by any code -- named for the real future lifecycle, not
-- implemented here). sqlite_path/port/jwt_secret are real, generated-once-per-tenant values --
-- jwt_secret is stored here (not just in the tenant's own env file) so a future re-provision/
-- restart can recover it without re-reading a file on disk that could have drifted.
CREATE TABLE IF NOT EXISTS tenants (
    id            INTEGER  PRIMARY KEY AUTOINCREMENT,
    org_name      VARCHAR(255) NOT NULL,
    contact_email VARCHAR(255) NOT NULL,
    subdomain     VARCHAR(100) NOT NULL UNIQUE,
    status        VARCHAR(32)  NOT NULL DEFAULT 'provisioning',
    port          INTEGER,
    sqlite_path   VARCHAR(500),
    jwt_secret    VARCHAR(255),
    error_message TEXT,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Reuses iduna.admin (the existing gate on this repo's own admin routes) for triggering
-- provisioning -- this is real, internal, sensitive infrastructure control (spins up a live
-- network service), not a separate product surface that needs its own narrower permission yet.
-- console.okemily.com's own eventual self-serve signup (unbuilt) will need a real, separate,
-- narrower-scoped path -- not this one.
