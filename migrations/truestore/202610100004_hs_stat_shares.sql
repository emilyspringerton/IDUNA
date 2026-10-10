-- A player's record with a deck only feeds that deck's PUBLIC win rate once they chose to share it
-- (publishing the deck shares; a private holder of an already-public deck can share just their stats).
-- Sharing is retroactive for that player+deck; unsharing removes it again. Nobody can tell from the
-- public numbers that someone is practicing the deck privately.
CREATE TABLE IF NOT EXISTS hs_stat_shares (
    owner_sub TEXT NOT NULL,
    deck_code TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (owner_sub, deck_code)
);
INSERT OR IGNORE INTO hs_stat_shares (owner_sub, deck_code)
  SELECT owner_sub, deckstring FROM hs_decks WHERE private=0 AND deleted=0;
