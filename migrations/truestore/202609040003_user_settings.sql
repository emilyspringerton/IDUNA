-- Real, general per-user settings home (WOTAN-24412, "IDUNA (and WOTAN) USER SETTINGS IN
-- GENERAL WE NEED A PLACE FOR THE USER TO CHANGE SETTINGS"), with the first real setting
-- (ACCESSABILITY-14441, "WE NEED A HIGH CONTRAST SETTING... TO MAKE IDUNA MORE HIGH CONTRAST
-- FOR VISUALLY ACCESSABILITY") living on it from day one. Real, typed columns, one row per
-- user -- matching this repo's own already-established convention (gfd_registration_settings'
-- own header comment: "one table per real feature, not a shared settings blob," checked
-- directly, no generic settings/feature-flag table exists anywhere in migrations/ before this).
-- A future new setting gets a new real column here, same discipline.
CREATE TABLE IF NOT EXISTS user_settings (
    user_id TEXT PRIMARY KEY, -- the JWT "sub" claim (middleware.SubjectFromContext) -- works for
                               -- both Google-auth and local-auth users, same identifier every
                               -- other per-user real table in this repo already keys on.
    high_contrast INTEGER NOT NULL DEFAULT 0, -- 0/1 boolean, SQLite has no native BOOLEAN type
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
