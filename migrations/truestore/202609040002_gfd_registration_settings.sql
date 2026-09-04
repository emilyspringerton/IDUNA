-- GFD registration waitlist toggle (kanban GFD-UA-001, second half: "need a toggle in iduna
-- back office to turn it into a waiting list once we have some initial testers"). Single-row
-- settings table, not a generic KV store -- this repo's own established convention is one
-- table per real feature, not a shared settings blob (checked directly: no existing generic
-- settings/feature-flag table anywhere in migrations/).
CREATE TABLE IF NOT EXISTS gfd_registration_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1), -- enforces exactly one row
    mode TEXT NOT NULL DEFAULT 'open',     -- 'open' | 'waitlist'
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT OR IGNORE INTO gfd_registration_settings (id, mode) VALUES (1, 'open');

-- Captures everything handleRegister would otherwise have used immediately, so approving a
-- waitlist entry later creates the real account without asking the player to re-register.
CREATE TABLE IF NOT EXISTS gfd_waitlist (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    email TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL,
    character_name TEXT NOT NULL DEFAULT '',
    character_job TEXT NOT NULL DEFAULT '',
    requested_at TEXT NOT NULL DEFAULT (datetime('now')),
    approved_at TEXT -- NULL until an admin converts this row into a real account
);
