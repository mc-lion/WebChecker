ALTER TABLE monitors
    ADD COLUMN retry_interval_seconds INT NOT NULL DEFAULT 10 AFTER interval_seconds;
