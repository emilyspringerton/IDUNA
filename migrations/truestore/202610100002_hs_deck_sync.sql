-- Decks synced from the live tracker at the end of a game. They land in the owner's library PRIVATE
-- (private=1); the owner publishes them with PATCH /decks/{id} {"private":false}. source_deck_id is
-- Hearthstone's own deck id (Decks.log "# Deck ID"), so a deck whose contents change gets a new copy.
ALTER TABLE hs_decks ADD COLUMN private INTEGER NOT NULL DEFAULT 0;
ALTER TABLE hs_decks ADD COLUMN source_deck_id TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_hs_decks_source ON hs_decks (owner_sub, source_deck_id);

-- Games played with a synced deck carry its library id, so a deck already has a win rate when it is first
-- published. Per-player settings live apart from the profile (a player can set them before picking a handle).
ALTER TABLE hs_games ADD COLUMN deck_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_hs_games_deck ON hs_games (deck_id);
CREATE TABLE IF NOT EXISTS hs_settings (
    sub          TEXT PRIMARY KEY,
    auto_publish INTEGER NOT NULL DEFAULT 0   -- 1 = synced decks are published immediately instead of private
);
