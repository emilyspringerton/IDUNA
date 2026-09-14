-- SHANKPIT sprays (S459-19, founder real-time: "can we implement sprays? from the 'projects' tab
-- in nock (the texture editor page) there should be a button export to spray goes to sprays
-- registry same treatment we need a sprays menun in native we need a nock sprays interface right
-- now just to set the default." Earlier, same thread: "WE NEED SPRAYS - SPRAY REGISTRY - CHOOSE
-- SPRAY - T SPRAYS YOUR DECAL ON THE WALL - CREATE SPRAYS FROM THE NOCK TEXTURE GENERATOR."
--
-- Real, same "many independent rows, no shared master" shape nock_textures already established
-- (see that migration's own doc comment) -- a spray is a real, independent, exported snapshot of
-- a Project's rendered PNG at the moment "Export to Spray" was pressed, NOT a live reference to a
-- nock_textures row (so deleting/regenerating the source texture later never breaks an already-
-- exported spray). png_data is a real BLOB, same convention.
--
-- is_default: exactly one spray may be the real, global default at a time (founder: "just to set
-- the default" -- a real, deliberate v0 narrowing, not full per-player spray selection persisted
-- server-side yet). Enforced in Go (SetDefaultSpray clears every other row's flag inside one
-- transaction), not a SQL constraint -- SQLite has no real partial-unique-index equivalent this
-- codebase already leans on elsewhere.
--
-- REAL, HONEST, DEFERRED (not this table, not this pass): a price/currency column for "sprays
-- shop for GFD glow" (founder: "when we have sprays shop for GFD glow we can configure the price
-- of the sprays or if they are free we will open up spray registry but not right now lets just
-- get it working") -- no pricing model exists anywhere in this schema yet.
CREATE TABLE IF NOT EXISTS shankpit_sprays (
    id         INTEGER  PRIMARY KEY AUTOINCREMENT,
    name       VARCHAR(200) NOT NULL,
    width      INTEGER  NOT NULL,
    height     INTEGER  NOT NULL,
    png_data   BLOB     NOT NULL,
    is_default BOOLEAN  NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_shankpit_sprays_name ON shankpit_sprays(name);
