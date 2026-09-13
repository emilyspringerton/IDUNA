-- BRAWLPIT online level editor (S415-02/03, founder real-time: "get the brawlpit level editor
-- online - web technologies - we already started building nock - can we finish building out
-- some of that interface so we can kind of parlay it into an online brawlpit level editor?").
--
-- A real, standalone-row CRUD store, mirroring nock_textures' own established shape (many
-- independent rows, no single "master" with derived views) -- reused here for a different game's
-- own domain object, kept in its own table/package rather than folded into internal/nock itself
-- (NOCK's own explicit design principle: stay a genuinely reusable engine tool, not coupled to
-- one specific game's concepts -- see internal/nock's own package doc for GFD's identical
-- treatment).
--
-- platforms_json stores an array of {x,y,w,h,type} objects -- the EXACT real shape
-- BRAWLPIT/packages/common/protocol.h's own Platform2D struct and
-- BRAWLPIT/packages/common/level_format.h's own JSON contract already define (S415-01, the real
-- blocking Phase 0 this depends on) -- x/y/w/h are world-unit floats, type is 0=SOLID/
-- 1=PASSTHROUGH. width/height are the web editor's own canvas bounds (in the same world units),
-- not part of the native export -- BRAWLPIT's own runtime loader doesn't read them; they exist
-- here purely to remember what canvas size a level was authored against so re-opening it for
-- editing restores the same view.
CREATE TABLE IF NOT EXISTS brawlpit_levels (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    name            VARCHAR(200) NOT NULL,
    width           REAL     NOT NULL DEFAULT 80,
    height          REAL     NOT NULL DEFAULT 40,
    platforms_json  TEXT     NOT NULL DEFAULT '[]',
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_brawlpit_levels_name ON brawlpit_levels(name);

-- Reuses iduna.admin, same gate every other /admin/nock route already uses -- no new permission
-- needed. Real, deliberate, named-not-silent scope choice (see BP_LEVEL_EDITOR_NORTHSTAR.md and
-- EMILY/BACKLOG.md SECTION 415): opening this to real, non-admin BRAWLPIT players needs the full
-- IDUNA-login-architecture decision that doc's own Phase 1 left open, deferred to cruise rather
-- than block this v0 slice on it.
