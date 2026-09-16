-- NOCK door script repository -- closing the "via the nock tools" gap named at the very start of
-- the SHANKPIT Story System thread (SHANKPIT/docs/STORY_SYSTEM_NORTHSTAR.md): SHANKPIT's real
-- Phase 1 door pipeline (S459-81) proved the PARENA->C->dlopen mechanism works, but scripts had
-- to be compiled by hand with a local `parena`+`gcc` toolchain and referenced by a local
-- filesystem path. This table is the real, server-side compile+store half, same real
-- BLOB-in-SQLite pattern nock_textures/nock_animations already established -- compiled_so is a
-- real BLOB (the actual dlopen-able shared object bytes SHANKPIT's server downloads and loads),
-- parena_source is kept alongside for real "edit and recompile" affordances (same role
-- nock_textures.parena_source already plays for procedural textures).
CREATE TABLE IF NOT EXISTS nock_door_scripts (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    name            VARCHAR(200) NOT NULL,
    parena_source   TEXT     NOT NULL,
    compiled_so     BLOB     NOT NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nock_door_scripts_name ON nock_door_scripts(name);

-- Reuses iduna.admin, same gate every other NOCK authoring surface already uses.
