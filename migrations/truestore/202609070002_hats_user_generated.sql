-- WOTAN_HAT_STORE_NORTHSTAR.md Phase 4.5 ("surprise box" -- spend Flow now, generate later at
-- use time via `emily promptoverse add ... --tag "promptoverse hat"`). Real, named schema gap
-- the NORTHSTAR doc's own Phase 4.5 section called out: today's `hats` rows are all
-- hand-curated (Phase 1's fixed 6-hat seed); a generated hat needs a way for the catalog to
-- tell curated and player-generated hats apart -- for display, moderation queueing, and any
-- future "only see hats you personally unlocked" filtering. Neither column is populated by
-- anything yet (no generator endpoint existed before this same migration's sibling handler
-- change) -- this is schema-first, matching this repo's own established "add the column, then
-- the code that uses it" migration convention.
ALTER TABLE hats ADD COLUMN user_generated INTEGER NOT NULL DEFAULT 0;
ALTER TABLE hats ADD COLUMN generated_by_character_id CHAR(36) DEFAULT NULL;
