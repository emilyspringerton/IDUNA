-- Drive sync audit log: records every Google Drive upload/list/download operation.
-- Append-only. Never update or delete rows.
-- Used by DriveHandler (/api/v1/drive/*) for audit trail and idempotency checks.

CREATE TABLE IF NOT EXISTS `drive_sync_log` (
    `id`              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `agent_id`        VARCHAR(36)     NOT NULL COMMENT 'FK to agents.id — who triggered the sync',
    `operation`       VARCHAR(64)     NOT NULL COMMENT 'upload | list | download',
    `drive_file_id`   VARCHAR(512)    NULL     COMMENT 'Google Drive file ID (null for list ops)',
    `filename`        VARCHAR(512)    NOT NULL COMMENT 'local filename or path',
    `file_size_bytes` BIGINT          NULL     COMMENT 'bytes transferred (null for list ops)',
    `folder_id`       VARCHAR(512)    NULL     COMMENT 'Google Drive folder ID',
    `web_view_link`   VARCHAR(1024)   NULL     COMMENT 'shareable Drive link (upload only)',
    `metadata`        JSON            NULL     COMMENT 'arbitrary context: training_run, epoch, etc.',
    `synced_at`       TIMESTAMP(6)    NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (`id`),
    INDEX `idx_drive_sync_agent`    (`agent_id`),
    INDEX `idx_drive_sync_op`       (`operation`),
    INDEX `idx_drive_sync_filename` (`filename`(255)),
    INDEX `idx_drive_sync_at`       (`synced_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
