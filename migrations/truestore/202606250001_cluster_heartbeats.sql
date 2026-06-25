-- S128-04: cluster heartbeats for federated multi-cluster EMILY
-- Each emily_cluster agent sends a heartbeat every 60s.
-- Active clusters: last_seen within 5 minutes.
CREATE TABLE IF NOT EXISTS cluster_heartbeats (
  agent_id      VARCHAR(36)   NOT NULL PRIMARY KEY,
  cluster_id    VARCHAR(128)  NOT NULL,
  capabilities  TEXT          NOT NULL DEFAULT '',  -- comma-separated: gpu,secwatch,prwatch
  load_score    FLOAT         NOT NULL DEFAULT 0.0, -- 0.0 = idle, 1.0 = saturated
  last_seen     DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  CONSTRAINT fk_heartbeat_agent FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
