-- NOCK animation repository (founder real-time, 2026-09-16, continuing the same session's own
-- "let's start iterating towards nock tools modeler (blender) and golden band" thread: "need
-- animation repository"). Real storage/browse layer for GOLDENBAND's own three real asset
-- formats (GOLDENBAND/format/GBAND_FORMAT.md, GSKEL_FORMAT.md, GMESH_FORMAT.md) -- the same real
-- BLOB-in-SQLite pattern nock_textures already established (202609120001_nock_textures.sql), not
-- reinvented: gband_data is required (every row IS an animation clip), gskel_data/gmesh_data are
-- nullable (a clip can be uploaded on its own, paired with an existing skeleton/mesh elsewhere,
-- or all three together from one `gbtool import --gltf` run -- see that command's own real,
-- fresh-this-session output shape). manifest_json holds the real .gband.json sidecar verbatim
-- (channels/authorship/tick_rate/etc.) rather than being split into columns -- same "richer,
-- tooling-facing, not the runtime's problem" rationale GBAND_FORMAT.md's own manifest section
-- already gives for keeping it JSON rather than a fixed binary layout.
CREATE TABLE IF NOT EXISTS nock_animations (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    name            VARCHAR(200) NOT NULL,
    tick_rate       INTEGER  NOT NULL,
    duration_ticks  INTEGER  NOT NULL,
    num_channels    INTEGER  NOT NULL,
    content_hash    VARCHAR(64) NOT NULL,  -- hex sha256, matches the .gband binary's own content_hash field
    gband_data      BLOB     NOT NULL,
    manifest_json   TEXT     NOT NULL,     -- the real .gband.json sidecar, verbatim
    gskel_data      BLOB,                  -- NULL unless a paired skeleton was uploaded alongside
    gmesh_data      BLOB,                  -- NULL unless a paired mesh was uploaded alongside
    source_location VARCHAR(200),          -- e.g. "gbtool import --gltf", "blender ci", free text
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nock_animations_name ON nock_animations(name);

-- Reuses iduna.admin, same gate nock_textures already uses -- no new permission needed.
