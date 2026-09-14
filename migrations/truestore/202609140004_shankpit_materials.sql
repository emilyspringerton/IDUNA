-- SHANKPIT NOCK level editor -- real Materials (S459-16, founder real-time: "for levels the
-- object blocks themselves that i place i would get a lot of value from being able to change
-- their texture - i think it makes sense to abstract into material first so it cleanly translates
-- into papercraft - so the default material is brick because thats the texture of blocks by
-- default ... we will want materials for concrete and wood ... we will need the ability to add
-- new materials and set their textures ... this is going to need to play with shaders too so
-- build that in from day 1"). A block's own `material` (a plain name, stored in its own
-- walls_json entry -- see the Wall struct's own new Material field) resolves to a row here.
--
-- texture_id (nullable) is a real, optional override into NOCK's own existing nock_textures
-- library (founder: "we will be able to manage the textures via the NOCK texture editor (not the
-- defaults)") -- null means "use the native built-in procedural default for this material name"
-- (brick/concrete/wood/metal each have a real proctex_make_*_rgba generator in SHANKPIT's own
-- packages/render/proc_tex.c). REAL, HONEST, NOT YET BUILT: the native client has no image
-- decoder (proc_tex.c generates RGBA procedurally, it doesn't read PNG files) -- a texture_id
-- override is stored and real, but the native loader does not fetch/decode it yet, only the NOCK
-- editor's own web preview can show it. Named, not silently faked.
--
-- specular/shininess are real Blinn-Phong shading parameters (founder, picking "real per-material
-- GLSL shading now" over a texture-only v0, then: "for the materials if you could have a some
-- kind of metal or shiny texture i dunno" / "vs0 are any correct real shaders") -- consumed by
-- apps/lobby's own new per-box specular-highlight shader pass. A material with near-zero specular
-- (brick/concrete/wood) costs that pass nothing extra to render (skipped below a real threshold).
--
-- REAL, DEFERRED, NAMED FUTURE WORK (v1, not this pass): an emissive/light-source material (founder:
-- "if we could make a material and quickly turn it into a flourescent light that would be amazing
-- ... think of that as vs1") -- no emissive column exists yet, this table only carries the real
-- surface-shading parameters VS0 actually uses.
-- shader_name (founder, direct: "build the shaders in to the native and then refer to the
-- shaders from NOCK directly? a shader registry too but for now it will just be like the ffi
-- names or whatever not the real shader code") -- a real, named reference into SHANKPIT's own
-- compiled-in shader registry (packages/render/material_shaders.h's own SHADER_* names). NOCK
-- only ever stores/edits this NAME, never GLSL source; the native client owns the real shader
-- implementation behind it. 'standard' is the one real, built-in shader VS0 ships (a real
-- Blinn-Phong specular-highlight pass) -- an emissive/light-source shader ("if we could make a
-- material and quickly turn it into a flourescent light") is real, named, deferred VS1 work, not
-- a row this migration seeds.
CREATE TABLE IF NOT EXISTS shankpit_materials (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT NOT NULL UNIQUE,
    shader_name TEXT NOT NULL DEFAULT 'standard',
    texture_id  INTEGER,
    specular    REAL NOT NULL DEFAULT 0,
    shininess   REAL NOT NULL DEFAULT 8,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT OR IGNORE INTO shankpit_materials (name, shader_name, specular, shininess) VALUES
    ('brick', 'standard', 0.04, 6),
    ('concrete', 'standard', 0.03, 4),
    ('wood', 'standard', 0.06, 8),
    ('metal', 'standard', 0.85, 64);
