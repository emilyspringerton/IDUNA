-- S508d: claim codes move from a plain 12-char code to a 25-char, dash-grouped format
-- (XXXXX-XXXXX-XXXXX-XXXXX-XXXXX, 29 characters including dashes) -- founder real-time: reads
-- better on a store page/Discord, matches Windows-product-key convention players already know.
-- SQLite doesn't enforce VARCHAR length at all (this is a no-op there), but the column width
-- must be correct for this table's real MySQL dialect too (the same file this schema's own
-- CREATE TABLE was authored against, per every other game_* table's own VARCHAR-per-dialect
-- convention).
ALTER TABLE game_claim_codes MODIFY COLUMN code VARCHAR(32) NOT NULL;
