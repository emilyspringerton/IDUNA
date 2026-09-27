-- NOCK sound library + shareable filter chains (founder real-time, 2026-09-27: "nock and shankpit
-- engine need sound engineering primatives we need a way to upload and record in nock as well as
-- pass filters around"). Same BLOB-in-SQLite pattern nock_textures/nock_animations established.
--
-- nock_sounds: one audio asset per row, stored exactly as uploaded/recorded (WAV, FLAC, MP3,
-- OGG/Opus, WebM/Opus from the browser's MediaRecorder). A render (a sound pushed through a
-- filter chain in NOCK) is saved as a new row pointing at its parent and the chain used, so
-- provenance survives.
--
-- nock_sound_filters: a named filter chain -- JSON list of stages whose parameters are the
-- PARENA stdlib/audio/dsp.prn primitives (biquad kinds, compressor/expander/de-esser/limiter
-- gain computers, loudness normalisation). Chains are first-class so they can be passed around:
-- cloned, exported/imported as JSON, and fetched by name by an engine (SHANKPIT) through the
-- public read-only route /api/v1/nock-sound-filters/<name>.
CREATE TABLE IF NOT EXISTS nock_sounds (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    name            VARCHAR(200) NOT NULL,
    mime_type       VARCHAR(100) NOT NULL,
    source          VARCHAR(20)  NOT NULL,  -- upload | record | render
    duration_ms     INTEGER  NOT NULL DEFAULT 0,  -- client-measured, 0 if unknown
    sample_rate     INTEGER  NOT NULL DEFAULT 0,
    channels        INTEGER  NOT NULL DEFAULT 0,
    size_bytes      INTEGER  NOT NULL,
    content_hash    VARCHAR(64) NOT NULL,  -- hex sha256 of audio_data
    audio_data      BLOB     NOT NULL,
    parent_id       INTEGER,               -- the sound a render was made from
    filter_id       INTEGER,               -- the chain a render was made with
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nock_sounds_name ON nock_sounds(name);

CREATE TABLE IF NOT EXISTS nock_sound_filters (
    id              INTEGER  PRIMARY KEY AUTOINCREMENT,
    name            VARCHAR(200) NOT NULL,
    description     TEXT     NOT NULL DEFAULT '',
    chain_json      TEXT     NOT NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_nock_sound_filters_name ON nock_sound_filters(name);
