-- heimdal_sprints: sprint planning records — human product requirements translated
-- by Emily Prime into RSI roadmap items and acceptance criteria.
-- Status lifecycle: pending -> queued -> in_progress -> complete | blocked

CREATE TABLE IF NOT EXISTS heimdal_sprints (
    id             BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    agent_name     VARCHAR(255) NOT NULL,
    requirement    TEXT NOT NULL,
    criteria_json  TEXT NOT NULL DEFAULT '[]',
    roadmap_id     VARCHAR(255) NOT NULL DEFAULT '',
    status         VARCHAR(32)  NOT NULL DEFAULT 'pending',
    apple_id       BIGINT NOT NULL DEFAULT 0,
    created_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_heimdal_sprints_status ON heimdal_sprints(status);
CREATE INDEX IF NOT EXISTS idx_heimdal_sprints_agent ON heimdal_sprints(agent_name, created_at);

-- permissions
INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000022', 'heimdal.submit',  'Submit sprint requirements via HEIMDAL planning interface'),
  ('00000002-0000-4000-8000-000000000023', 'heimdal.process', 'Process HEIMDAL sprints and update status (Emily Prime only)');

-- super_admin gets both
INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000022'),
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000023');
