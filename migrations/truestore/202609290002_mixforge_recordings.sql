-- S513: storage for MIXFORGE's client-side mix recorder (game_online.go's recordingCreate/
-- recordingsList/recordingDownload). Audio stored exactly as the browser's MediaRecorder produced
-- it (WebM/Opus or OGG), as a BLOB on the row -- same real, days-prior precedent nock_sounds
-- (202609270002_nock_sounds.sql) already established for this exact "browser-recorded audio"
-- shape, not a new blob-directory-on-disk convention.
--
-- `game` is carried (not assumed to always be "mixforge") in case another game ever wants the
-- same "save a recorded clip to your IDUNA account" resource -- same generalization instinct
-- brawlpit.CheckpointStore's own `Game` column already applies to checkpoints.
CREATE TABLE IF NOT EXISTS mixforge_recordings (
    id          VARCHAR(36)  PRIMARY KEY,
    game        VARCHAR(40)  NOT NULL,
    player_id   VARCHAR(64)  NOT NULL,
    name        VARCHAR(200) NOT NULL,
    mime_type   VARCHAR(100) NOT NULL,
    size_bytes  INTEGER      NOT NULL,
    audio_data  BLOB         NOT NULL,
    created_at  DATETIME     NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_mixforge_recordings_player ON mixforge_recordings(game, player_id, created_at DESC);
