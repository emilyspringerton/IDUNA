-- NOCK animator (founder real-time, 2026-09-27: "continue to evolve NOCK tools into a total
-- blender replacement we need a way to animate in NOCK"). keyframes_json holds the editable
-- keyframe source (internal/nock/keyframes.go's KeyframeDoc) for a clip authored in NOCK. The
-- baked .gband in gband_data is derived from it on save, the same source → artifact split the
-- procedural texture library keeps (PARENA source next to the rendered PNG). NULL for imported
-- clips, which have no keyframe source; the animator derives editable keys from their baked
-- data on demand instead (KeyframesFromClip).
ALTER TABLE nock_animations ADD COLUMN keyframes_json TEXT;
