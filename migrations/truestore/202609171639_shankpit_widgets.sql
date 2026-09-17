-- SHANKPIT NOCK level editor -- real Widgets (S482, founder real-time, direct correction of the
-- earlier S479-follow-up door-composition work: "i still dont know how to add a door... i dont
-- want to make doors be levels please - make widget or something they are both objects but
-- widgets just dont show up in the levels menu and the geometry of the widget shows up not the
-- geometry of the underlying level under the widget - there should be no ground plane and no
-- dimension in the widget - a level is a dimension - a widget is just a widget."
--
-- A Widget is walls + doors only -- no width/height/depth, no ground_plane, no spawners/
-- nav_nodes/characters/level_exits/next_level_id/is_story_start/is_default_queue. It is a real,
-- separate table (not a flag on shankpit_levels) precisely because it is NOT a level: it never
-- appears in the Levels menu, never has a dimension, and (v0, real, deliberate scope limit) never
-- itself contains further placed Objects -- a widget is a leaf-level reusable geometry piece, not
-- a recursively-composable one like a level. See internal/shankpit/widget_store.go and
-- LevelObject's own new RefWidgetID field (the real placement mechanism: a level's own Objects
-- array can reference EITHER a level OR a widget, never both, by which id field is set).
CREATE TABLE IF NOT EXISTS shankpit_widgets (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    name        VARCHAR(200) NOT NULL,
    walls_json  TEXT     NOT NULL DEFAULT '[]',
    doors_json  TEXT     NOT NULL DEFAULT '[]',
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_shankpit_widgets_name ON shankpit_widgets(name);

-- Objects can now reference a widget instead of a level -- exactly one of ref_level_id/
-- ref_widget_id is ever set (enforced in Go, validateObjects, not a SQL CHECK constraint, same
-- real "structural validation lives in Go" convention every other shankpit_levels JSON column
-- already follows -- objects_json itself is untyped TEXT to SQLite).
