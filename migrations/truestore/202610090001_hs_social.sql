-- WOTAN HEARTHSTONE_NORTHSTAR.md §4: Hearthstone deck sharing + basic socials (profile/handle, wall, follow, likes,
-- comments, reports). Keyed by the IDUNA JWT "sub" (one IDUNA account = one HS profile). Text is stored raw and
-- HTML-escaped by clients on render. hs_cards is created now but intentionally EMPTY -- the card name/art backfill
-- (client data extraction) is the founder's explicit last step.

CREATE TABLE IF NOT EXISTS hs_profiles (
    sub        TEXT PRIMARY KEY,
    handle     TEXT NOT NULL UNIQUE COLLATE NOCASE,
    bio        TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS hs_decks (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_sub      TEXT NOT NULL,
    title          TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    class          TEXT NOT NULL,
    format         TEXT NOT NULL,
    year           TEXT NOT NULL DEFAULT '',
    hero_dbf       INTEGER NOT NULL DEFAULT 0,
    deckstring     TEXT NOT NULL,
    cards_json     TEXT NOT NULL DEFAULT '[]',   -- display list [{name,cost,count}] from the paste
    dbf_json       TEXT NOT NULL DEFAULT '[]',   -- identity list [{dbf_id,count}] from the code
    card_count     INTEGER NOT NULL,
    names_resolved INTEGER NOT NULL DEFAULT 0,
    likes          INTEGER NOT NULL DEFAULT 0,
    comments       INTEGER NOT NULL DEFAULT 0,
    deleted        INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at     TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (owner_sub, deckstring)
);
CREATE INDEX IF NOT EXISTS idx_hs_decks_new   ON hs_decks (deleted, id DESC);
CREATE INDEX IF NOT EXISTS idx_hs_decks_owner ON hs_decks (owner_sub, deleted);
CREATE INDEX IF NOT EXISTS idx_hs_decks_class ON hs_decks (class, format);

CREATE TABLE IF NOT EXISTS hs_deck_likes (
    deck_id INTEGER NOT NULL,
    sub     TEXT NOT NULL,
    PRIMARY KEY (deck_id, sub)
);

CREATE TABLE IF NOT EXISTS hs_deck_comments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    deck_id    INTEGER NOT NULL,
    sub        TEXT NOT NULL,
    body       TEXT NOT NULL,
    deleted    INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_hs_deck_comments ON hs_deck_comments (deck_id, deleted, id);

CREATE TABLE IF NOT EXISTS hs_wall_posts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_sub  TEXT NOT NULL,
    body       TEXT NOT NULL,
    likes      INTEGER NOT NULL DEFAULT 0,
    comments   INTEGER NOT NULL DEFAULT 0,
    deleted    INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_hs_wall_owner ON hs_wall_posts (owner_sub, deleted, id DESC);
CREATE INDEX IF NOT EXISTS idx_hs_wall_feed  ON hs_wall_posts (deleted, id DESC);

CREATE TABLE IF NOT EXISTS hs_post_likes (
    post_id INTEGER NOT NULL,
    sub     TEXT NOT NULL,
    PRIMARY KEY (post_id, sub)
);

CREATE TABLE IF NOT EXISTS hs_post_comments (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    post_id    INTEGER NOT NULL,
    sub        TEXT NOT NULL,
    body       TEXT NOT NULL,
    deleted    INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_hs_post_comments ON hs_post_comments (post_id, deleted, id);

CREATE TABLE IF NOT EXISTS hs_follows (
    follower_sub TEXT NOT NULL,
    followee_sub TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (follower_sub, followee_sub)
);
CREATE INDEX IF NOT EXISTS idx_hs_follows_followee ON hs_follows (followee_sub);

CREATE TABLE IF NOT EXISTS hs_reports (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    reporter_sub TEXT NOT NULL,
    kind         TEXT NOT NULL,
    target_id    TEXT NOT NULL,
    reason       TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Intentionally empty until the card-data backfill (WOTAN HEARTHSTONE_NORTHSTAR.md HS-10).
CREATE TABLE IF NOT EXISTS hs_cards (
    dbf_id    INTEGER PRIMARY KEY,
    name      TEXT NOT NULL,
    cost      INTEGER NOT NULL DEFAULT 0,
    class     TEXT NOT NULL DEFAULT '',
    rarity    TEXT NOT NULL DEFAULT '',
    card_set  TEXT NOT NULL DEFAULT '',
    text      TEXT NOT NULL DEFAULT '',
    image_url TEXT NOT NULL DEFAULT ''
);
