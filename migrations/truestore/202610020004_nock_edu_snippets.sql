-- NOCK EduVM snippets (card T83300158 / #495): saveable EduScript snippets attached to widgets, the same
-- "authored in NOCK, fetched by the game" shape nock_textures / nock_door_scripts already use. A snippet is plain
-- EduScript text (SHANKPIT packages/education, the Architect's Orb VM) -- there is nothing to compile server-side
-- (the VM is sandboxed and capability-limited in the game), so unlike door scripts no compiled blob is stored.
-- widget_name names the NOCK widget the snippet is attached to (e.g. ORB, or any placed widget).
CREATE TABLE IF NOT EXISTS nock_edu_snippets (
    id          INTEGER  PRIMARY KEY AUTOINCREMENT,
    name        VARCHAR(64)  NOT NULL,
    widget_name VARCHAR(64)  NOT NULL,
    source      TEXT     NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nock_edu_snippets_widget_name ON nock_edu_snippets(widget_name, name);
-- Reuses iduna.admin for authoring, same gate every other NOCK authoring surface uses.
