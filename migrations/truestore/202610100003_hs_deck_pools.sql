-- Win/loss pools per deck CONTENTS (deckstring), not per library row:
--   * identical decks of different players share one pool; private holders see the private pool
--     (games played while their copy was private), a public deck shows only the public pool;
--   * identical public decks are one deck: the first to publish picks the name, later publishers are
--     merged into it (merged_into) and their later games count to the public pool.
ALTER TABLE hs_games ADD COLUMN deck_code TEXT NOT NULL DEFAULT '';
ALTER TABLE hs_games ADD COLUMN deck_public INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_hs_games_pool ON hs_games (deck_code, deck_public);
UPDATE hs_games SET deck_code=(SELECT deckstring FROM hs_decks WHERE hs_decks.id=hs_games.deck_id),
                    deck_public=(SELECT 1-private FROM hs_decks WHERE hs_decks.id=hs_games.deck_id)
 WHERE deck_id>0;
ALTER TABLE hs_decks ADD COLUMN merged_into INTEGER NOT NULL DEFAULT 0;
ALTER TABLE hs_decks ADD COLUMN published_at TEXT NOT NULL DEFAULT '';
UPDATE hs_decks SET published_at=created_at WHERE private=0;
CREATE INDEX IF NOT EXISTS idx_hs_decks_code ON hs_decks (deckstring, private, deleted);
