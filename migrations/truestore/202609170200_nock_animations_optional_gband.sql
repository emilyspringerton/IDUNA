-- Real, live-found gap (founder real-time, 2026-09-17): "Error: 422: {\"error\":\"glTF import
-- failed: no animation found in this file -- the animation repository requires at least one
-- animated (translation or rotation) channel...\"}" -- a real, uploaded rigged mesh (a mannequin,
-- mesh + skeleton, no baked animation) couldn't be imported at all, because nock_animations was
-- built animation-first-class: gband_data/content_hash/tick_rate/duration_ticks/num_channels
-- were all NOT NULL. A rigged mesh with no animation yet (or a bare skeleton, or a bare mesh) is
-- a real, common, legitimate asset on its own -- this table now stores it too, matching
-- GOLDENBAND's own real three-part asset model (.gmesh geometry / .gskel skeleton / .gband
-- motion, each independently real and independently useful) instead of forcing every real upload
-- to have all three.
--
-- SQLite can't drop a NOT NULL constraint in place -- real, standard recreate-copy-swap, same
-- technique this monorepo's own migrations already use for this shape of change. Existing real
-- rows (as of this migration: one real, live, founder-uploaded animation) are preserved exactly.
CREATE TABLE nock_animations_new (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    name            VARCHAR(200) NOT NULL,
    tick_rate       INTEGER,          -- NULL if this asset has no animation (mesh/skeleton only)
    duration_ticks  INTEGER,          -- NULL if this asset has no animation
    num_channels    INTEGER,          -- NULL if this asset has no animation
    content_hash    VARCHAR(64),      -- NULL if this asset has no animation
    gband_data      BLOB,             -- NULL if this asset has no animation
    manifest_json   TEXT,             -- NULL if this asset has no animation
    gskel_data      BLOB,             -- NULL unless a paired skeleton was uploaded alongside
    gmesh_data      BLOB,             -- NULL unless a paired mesh was uploaded alongside
    source_location VARCHAR(200),
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO nock_animations_new SELECT * FROM nock_animations;
DROP TABLE nock_animations;
ALTER TABLE nock_animations_new RENAME TO nock_animations;
CREATE UNIQUE INDEX IF NOT EXISTS idx_nock_animations_name ON nock_animations(name);
