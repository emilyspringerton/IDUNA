-- SHANKPIT NOCK cube styles (founder real-time, 2026-10-16: "cube styles like indesign ... set the style
-- on a cube and then update the style and all the cubes update ... a checkbox on the level style to turn
-- it into a global style ... clone styles so you can fork them into local styles or new global styles").
--
-- One table for both scopes: level_id NULL = a GLOBAL style (every level can use it); level_id set = a LOCAL
-- style that belongs to that one level. A wall points at its style with style_id (inside walls_json);
-- LevelStore.GetLevel overlays the style's current material/colour/friction onto every wall that points at
-- it, so editing a style updates every cube (and, for a global style, every level) with no re-save.
CREATE TABLE IF NOT EXISTS shankpit_cube_styles (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    level_id   INTEGER,
    material   TEXT    NOT NULL DEFAULT 'brick',
    r          REAL    NOT NULL DEFAULT 0.6,
    g          REAL    NOT NULL DEFAULT 0.6,
    b          REAL    NOT NULL DEFAULT 0.6,
    friction   REAL    NOT NULL DEFAULT 0.3,
    created_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT    NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_shankpit_cube_styles_scope_name ON shankpit_cube_styles (COALESCE(level_id, 0), name);
