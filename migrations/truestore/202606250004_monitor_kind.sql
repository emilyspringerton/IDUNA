-- Monitor kind column: heartbeat (default) | cron | deadman
-- heartbeat: alert if no check-in within timeout+grace
-- cron:      same semantics; indicates a scheduled job, ready for future cron-expression support
-- deadman:   no grace period; alert immediately after timeout (zero-tolerance)
ALTER TABLE monitors ADD COLUMN kind VARCHAR(16) NOT NULL DEFAULT 'heartbeat';
