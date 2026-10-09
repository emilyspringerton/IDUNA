-- Hearthstone game records uploaded by the HRIP deck tracker (one row per finished game).
-- card_id (string id, e.g. HERO_08) lets game records resolve cards/classes without dbf ids.
ALTER TABLE hs_cards ADD COLUMN card_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_hs_cards_card_id ON hs_cards (card_id);

CREATE TABLE IF NOT EXISTS hs_games (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    owner_sub   TEXT NOT NULL,
    fingerprint TEXT NOT NULL,            -- sha256 of the uploaded record; re-uploads are no-ops
    game_type   TEXT NOT NULL DEFAULT '',
    format      TEXT NOT NULL DEFAULT '',
    me          INTEGER NOT NULL,         -- 1 or 2: the uploader's player slot
    result      INTEGER NOT NULL DEFAULT 0, -- uploader's result: 1 won, 2 lost, 3 tied, 0 unknown
    turns       INTEGER NOT NULL DEFAULT 0,
    complete    INTEGER NOT NULL DEFAULT 0, -- 1 finished, 2 abandoned
    my_class    TEXT NOT NULL DEFAULT '',
    opp_class   TEXT NOT NULL DEFAULT '',
    my_name     TEXT NOT NULL DEFAULT '',
    opp_name    TEXT NOT NULL DEFAULT '',
    opp_cards   TEXT NOT NULL DEFAULT '[]', -- distinct card ids the opponent revealed (deck inference)
    record_json TEXT NOT NULL,             -- full tracker record incl. replay timeline
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (owner_sub, fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_hs_games_owner   ON hs_games (owner_sub, id DESC);
CREATE INDEX IF NOT EXISTS idx_hs_games_matchup ON hs_games (my_class, opp_class, result);
