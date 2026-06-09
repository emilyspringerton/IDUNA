-- Push tokens: FCM device tokens registered by MJOLNIR Android clients.
-- Upserted on each app launch. Emily Prime resolves the token to fire FCM pushes.

CREATE TABLE IF NOT EXISTS `push_tokens` (
    `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `agent_name`      VARCHAR(128)    NOT NULL COMMENT 'logical name of the device owner agent (e.g. mjolnir-emily)',
    `platform`        VARCHAR(32)     NOT NULL DEFAULT 'android' COMMENT 'android | ios',
    `fcm_token`       TEXT            NOT NULL COMMENT 'Firebase Cloud Messaging registration token',
    `fingerprint`     VARCHAR(128)    NOT NULL DEFAULT '' COMMENT 'SHA-256 of device Build.FINGERPRINT for dedup',
    `registered_at`   TIMESTAMP(6)    NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    `last_used_at`    TIMESTAMP(6)    NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`id`),
    UNIQUE KEY `uq_push_tokens_name_fp` (`agent_name`, `fingerprint`),
    INDEX `idx_push_tokens_agent` (`agent_name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Permission for push token management
INSERT IGNORE INTO permissions (id, name, description) VALUES
  ('00000002-0000-4000-8000-000000000020', 'push_tokens.write', 'Register and update FCM push tokens'),
  ('00000002-0000-4000-8000-000000000021', 'push_tokens.read',  'Read FCM push tokens (Emily Prime only)');

-- super_admin gets both
INSERT IGNORE INTO role_permissions (role_id, permission_id) VALUES
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000020'),
  ('00000001-0000-4000-8000-000000000001', '00000002-0000-4000-8000-000000000021');
