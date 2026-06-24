-- GFD subscription tier definitions (S124-02).
-- Tiers define GoblinFoxDragon.com access levels.
-- features column: JSON array of feature flag strings.
CREATE TABLE IF NOT EXISTS subscription_tiers (
    tier_id      TEXT PRIMARY KEY,              -- free_trial | frequency_monthly | frequency_annual | bloc_annual
    name         TEXT NOT NULL,
    monthly_usd  REAL NOT NULL DEFAULT 0,
    annual_usd   REAL NOT NULL DEFAULT 0,
    features     TEXT NOT NULL DEFAULT '[]',   -- JSON array of strings
    active       INTEGER NOT NULL DEFAULT 1,   -- 1 = offered; 0 = retired
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Seed canonical tiers.
INSERT OR IGNORE INTO subscription_tiers (tier_id, name, monthly_usd, annual_usd, features) VALUES
    ('free_trial',         'Free Trial',       0,     0,     '["detroit_apartment","basic_mud","phone_tab1","forums"]'),
    ('frequency_monthly',  'The Frequency',    12,    0,     '["all_districts","all_classes","full_phone","cast_terminal","timeline","priority_support"]'),
    ('frequency_annual',   'The Frequency (Annual)', 10, 120, '["all_districts","all_classes","full_phone","cast_terminal","timeline","priority_support","annual_billing"]'),
    ('bloc_annual',        'The Bloc',         0,     96,    '["all_districts","all_classes","full_phone","cast_terminal","timeline","guild_tools","bloc_faction_bonus","fo_defense_npcs","priority_support","annual_billing"]');

-- Extend user_subscriptions to reference tier_id.
-- tier_id NULL means legacy plan field is used.
ALTER TABLE user_subscriptions ADD COLUMN tier_id TEXT REFERENCES subscription_tiers(tier_id);
ALTER TABLE user_subscriptions ADD COLUMN stripe_customer_id TEXT;
ALTER TABLE user_subscriptions ADD COLUMN stripe_subscription_id TEXT;
ALTER TABLE user_subscriptions ADD COLUMN trial_ends_at TEXT;

-- Stripe webhook events log.
CREATE TABLE IF NOT EXISTS stripe_events (
    id           TEXT PRIMARY KEY,             -- Stripe event ID (evt_...)
    type         TEXT NOT NULL,               -- e.g. customer.subscription.updated
    user_id      TEXT,
    payload      TEXT NOT NULL DEFAULT '{}',  -- full event JSON
    processed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX IF NOT EXISTS idx_stripe_events_user ON stripe_events(user_id);
CREATE INDEX IF NOT EXISTS idx_stripe_events_type ON stripe_events(type);
