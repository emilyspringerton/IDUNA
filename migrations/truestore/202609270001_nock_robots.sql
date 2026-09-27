-- NOCK robot registry (founder real-time, 2026-09-27: "upgrade shankpit and nock to formal rigid
-- body physics we need to get goldenband rigged up with real robot data from industrial data
-- sheets"). Stores GOLDEN BAND datasheet robot rigs (GOLDENBAND/format/GROBOT_FORMAT.md): the
-- human-readable, source-cited spec (.grobot.json, verbatim), the compiled physics rig (.grobot
-- binary -- per-link mass/COM/principal inertia, per-joint limits/max speed/max torque) and the
-- optional display skeleton (.gskel). spec_hash is the sha256 of spec_json; the upload handler
-- only accepts a .grobot whose own embedded spec_hash matches it, so a stored physics rig is
-- always provably compiled from the cited spec stored next to it. Same BLOB-in-SQLite pattern as
-- nock_textures / nock_animations; same iduna.admin gate.
CREATE TABLE IF NOT EXISTS nock_robots (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    name            VARCHAR(200) NOT NULL,
    manufacturer    VARCHAR(200) NOT NULL,
    model           VARCHAR(200) NOT NULL,
    joint_count     INTEGER  NOT NULL,
    moving_mass_kg  REAL     NOT NULL,
    spec_hash       VARCHAR(64) NOT NULL,
    spec_json       TEXT     NOT NULL,
    grobot_data     BLOB     NOT NULL,
    gskel_data      BLOB,
    source_location VARCHAR(200),
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nock_robots_name ON nock_robots(name);
