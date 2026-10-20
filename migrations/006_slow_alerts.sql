-- Per-monitor slow alerts: enable flag, separate threshold, consecutive_slow counter.
SET @col_exists := (
    SELECT COUNT(*)
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'monitors'
      AND column_name = 'slow_alerts_enabled'
);
SET @ddl := IF(@col_exists = 0,
    'ALTER TABLE monitors ADD COLUMN slow_alerts_enabled TINYINT(1) NOT NULL DEFAULT 1 AFTER fail_threshold',
    'DO 0');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
    SELECT COUNT(*)
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'monitors'
      AND column_name = 'slow_fail_threshold'
);
SET @ddl := IF(@col_exists = 0,
    'ALTER TABLE monitors ADD COLUMN slow_fail_threshold INT NOT NULL DEFAULT 3 AFTER slow_alerts_enabled',
    'DO 0');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;

SET @col_exists := (
    SELECT COUNT(*)
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'alert_states'
      AND column_name = 'consecutive_slow'
);
SET @ddl := IF(@col_exists = 0,
    'ALTER TABLE alert_states ADD COLUMN consecutive_slow INT NOT NULL DEFAULT 0 AFTER consecutive_problems',
    'DO 0');
PREPARE stmt FROM @ddl;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;
