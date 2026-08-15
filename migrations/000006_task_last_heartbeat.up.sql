ALTER TABLE tasks
    ADD COLUMN last_heartbeat_at DATETIME(6) NULL AFTER lease_expires_at;
