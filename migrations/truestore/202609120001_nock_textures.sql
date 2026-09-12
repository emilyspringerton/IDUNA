-- NOCK texture library (founder real-time, 2026-09-12: "we are making a texture generator and
-- manager so it needs to have CRUD and all that and also we are gonna want to save them in
-- sqlite or whatever with their parena src... we can build the human manual photoshop
-- affordances after we get some basic texture management primitives built into the iduna nock
-- console"). A real re-scope: internal/nock's own Project/Layer flat-file compositing engine
-- (docs/NOCK_NORTHSTAR.md) stays as-is for now, but the primary managed entity going forward is
-- a standalone Texture, not a Project -- this table is the real, SQLite-backed store for it.
--
-- parena_source/prompt are both nullable: a texture saved from a plain image upload or a
-- Project export has neither; a texture generated via NOCK's Vertex AI pipeline
-- (internal/nock/gen_vertex.go) has both. png_data is a real BLOB, not base64-in-TEXT (the
-- MySQL-side camera_observations table's own convention) -- SQLite's native BLOB type and Go's
-- database/sql []byte handling are both real and already proven elsewhere in this codebase
-- (modernc.org/sqlite), and base64 would only add ~33% storage overhead with no real benefit
-- here.
--
-- Clone (founder real-time, same session, referencing CarePyre's own real master-resume/clone
-- feature -- COMMUNITY_TOOLS_RESUME_NORTHSTAR.md -- as the model, with one real, named
-- difference: resumes have exactly one master per user with many derived target VIEWS
-- (subset + override of that one master); textures have no single master at all -- "there wont
-- be just one master texture there will be many master textures." A clone here is therefore a
-- full, independent row copy (new id, own png_data/parena_source), not a view-with-overrides
-- referencing a shared parent -- there is no parent_id column because nothing here is a view of
-- anything else. If provenance ("cloned from X") ever needs tracking, that is real, separate,
-- deliberately deferred future work, not silently assumed unnecessary.
CREATE TABLE IF NOT EXISTS nock_textures (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    name            VARCHAR(200) NOT NULL,
    width           INTEGER  NOT NULL,
    height          INTEGER  NOT NULL,
    png_data        BLOB     NOT NULL,
    parena_source   TEXT,             -- NULL unless procedurally generated
    prompt          TEXT,             -- NULL unless generated from a text prompt (Vertex)
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nock_textures_name ON nock_textures(name);

-- Reuses iduna.admin, same gate every other /admin/nock route already uses -- no new permission
-- needed.
