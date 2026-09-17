-- SHANKPIT NOCK Materials -- real per-material friction (S478b, founder real-time: "right now the
-- physics are so slippery it can be unreasonably hard to parkour" -> "make the material friction
-- stuff working per cube" -> "do we need to make friction per material?"). Before this migration,
-- friction was ONLY ever a global constant in native code (packages/common/physics.h's own
-- FRICTION) -- LevelBox.friction (a per-box JSON field) was parsed but never actually consumed by
-- collision (level_boxes.h's own doc comment named this honestly since S459-16). Real, deliberate
-- choice: friction resolves per-MATERIAL, not per-box, matching how shader_name/specular/shininess
-- already work in this same table -- a level author picks a material per block (brick/concrete/
-- wood/metal/...), not a bespoke friction number per block.
--
-- Default 0.30 matches packages/common/physics.h's own just-bumped FRICTION baseline exactly, so
-- every existing material (and any level using the default 'brick' material) feels identical to
-- the new global baseline until an author deliberately picks a stickier/slicker material.
ALTER TABLE shankpit_materials ADD COLUMN friction REAL NOT NULL DEFAULT 0.30;

-- Real, seeded per-material feel: metal is classically slicker than brick/concrete/wood -- gives
-- the new mechanic actual gameplay texture instead of every seeded material behaving identically.
UPDATE shankpit_materials SET friction = 0.30 WHERE name IN ('brick', 'concrete');
UPDATE shankpit_materials SET friction = 0.34 WHERE name = 'wood';
UPDATE shankpit_materials SET friction = 0.14 WHERE name = 'metal';
