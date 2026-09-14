-- SHANKPIT NOCK level editor -- real ground plane (S459-08, founder real-time: "i want there to
-- be a plane by default that the player collides with - the checkerboard in the level editor -
-- that should constitute the plane for that level... configurable in terms of size... turn on
-- able and off able per level"). A real, first-class, per-level, persisted property -- not a
-- Wall, not a hardcoded engine default.
--
-- ground_plane_squares is a real SQUARE COUNT, not a raw world-unit length -- founder, direct:
-- "the squares are always the same size" / "so the units needs to be the number of squares in
-- the grid." internal/shankpit.GridCellSize (Go) is the fixed, constant per-square world-unit
-- size every level shares, matched directly against SHANKPIT's own real, already-existing
-- `#define GRID_SIZE 50.0f` (apps/lobby/src/main.c) -- the same real grid its own already-working
-- magenta-glow footstep trail effect already keys off of, not an arbitrary new constant.
ALTER TABLE shankpit_levels ADD COLUMN ground_plane_enabled BOOLEAN NOT NULL DEFAULT 1;
ALTER TABLE shankpit_levels ADD COLUMN ground_plane_squares INTEGER NOT NULL DEFAULT 2;
