-- Check-in monitors: unique URL heartbeat/dead-man-switch alerting.
-- Each monitor has a slug (the check-in URL token), a timeout, and alert config.
-- status: unknown | healthy | failing
CREATE TABLE IF NOT EXISTS monitors (
  id                  INTEGER AUTO_INCREMENT PRIMARY KEY,
  name                VARCHAR(255)  NOT NULL,
  slug                VARCHAR(64)   NOT NULL,
  timeout_seconds     INTEGER       NOT NULL DEFAULT 3600,
  grace_seconds       INTEGER       NOT NULL DEFAULT 60,
  owner               VARCHAR(255)  NOT NULL DEFAULT '',
  last_checkin_at     DATETIME,
  alerted_at          DATETIME,
  alert_slack_channel VARCHAR(255)  NOT NULL DEFAULT '',
  alert_email         VARCHAR(255)  NOT NULL DEFAULT '',
  status              VARCHAR(16)   NOT NULL DEFAULT 'unknown',
  created_at          DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at          DATETIME      NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE KEY uq_monitors_slug (slug)
);
