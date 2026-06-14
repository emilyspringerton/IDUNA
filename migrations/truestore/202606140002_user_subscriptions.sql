-- User subscription table. Tracks Emily+ subscription status per IDUNA user.
-- status: active | cancelled | expired
-- When status=active AND expires_at IS NULL OR expires_at > NOW(), user has cap.query.full.
CREATE TABLE IF NOT EXISTS user_subscriptions (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     TEXT NOT NULL UNIQUE,
    plan        TEXT NOT NULL DEFAULT 'emily_plus',
    status      TEXT NOT NULL DEFAULT 'active',
    expires_at  TEXT,           -- RFC3339 UTC; NULL = perpetual (manual grant)
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    FOREIGN KEY (user_id) REFERENCES users(id)
);

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_user_id ON user_subscriptions(user_id);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_status  ON user_subscriptions(status);
