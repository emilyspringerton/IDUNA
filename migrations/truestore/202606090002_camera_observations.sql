-- camera_observations: images submitted by MJOLNIR for Emily Prime intelligence analysis.
-- Status lifecycle: pending -> processing -> done | error
-- Image stored as base64 text; analysis filled in by Emily Prime after vision processing.

CREATE TABLE IF NOT EXISTS camera_observations (
    id            BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    agent_name    VARCHAR(255) NOT NULL,
    image_data    MEDIUMTEXT NOT NULL,          -- base64-encoded image
    media_type    VARCHAR(64) NOT NULL DEFAULT 'image/jpeg',
    prompt        TEXT,                          -- optional user-supplied context
    analysis      TEXT,                          -- filled by Emily Prime
    apple_id      BIGINT,                        -- IDUNA Apple ID for the analysis Apple
    status        VARCHAR(32) NOT NULL DEFAULT 'pending',
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    processed_at  DATETIME
);

CREATE INDEX IF NOT EXISTS idx_camera_obs_agent_status ON camera_observations(agent_name, status);
CREATE INDEX IF NOT EXISTS idx_camera_obs_created ON camera_observations(created_at DESC);

-- permissions
INSERT OR IGNORE INTO permissions(name, description) VALUES
    ('intelligence.observe', 'Submit a camera observation for analysis'),
    ('intelligence.read',    'Read camera observation results');

-- grant to emily agent role
INSERT OR IGNORE INTO role_permissions(role_id, permission_name)
SELECT r.id, p.name
FROM roles r, permissions p
WHERE r.name IN ('emily_prime', 'emily_agent', 'agent_default')
  AND p.name IN ('intelligence.observe', 'intelligence.read');
