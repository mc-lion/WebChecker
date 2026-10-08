-- Справочник User-Agent и привязка к монитору.
CREATE TABLE IF NOT EXISTS user_agents (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    value VARCHAR(512) NOT NULL,
    is_default TINYINT(1) NOT NULL DEFAULT 0,
    sort_order INT NOT NULL DEFAULT 0,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO user_agents (name, value, is_default, sort_order)
SELECT name, value, is_default, sort_order FROM (
    SELECT 'Chrome (Windows)' AS name,
           'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36' AS value,
           1 AS is_default,
           1 AS sort_order
    UNION ALL SELECT 'Firefox (Windows)',
           'Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0',
           0, 2
    UNION ALL SELECT 'Safari (macOS)',
           'Mozilla/5.0 (Macintosh; Intel Mac OS X 14_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15',
           0, 3
    UNION ALL SELECT 'Chrome (Android)',
           'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36',
           0, 4
    UNION ALL SELECT 'webChecker',
           'webChecker/1.0',
           0, 5
) AS seed
WHERE (SELECT COUNT(*) FROM user_agents) = 0;

SET @col_exists := (
    SELECT COUNT(*)
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'monitors'
      AND column_name = 'user_agent_id'
);
SET @ddl := IF(@col_exists = 0,
    'ALTER TABLE monitors ADD COLUMN user_agent_id BIGINT NULL AFTER fail_threshold',
    'DO 0');
PREPARE apply_ua_column FROM @ddl;
EXECUTE apply_ua_column;
DEALLOCATE PREPARE apply_ua_column;

UPDATE monitors
SET user_agent_id = (SELECT id FROM user_agents WHERE is_default = 1 ORDER BY id LIMIT 1)
WHERE user_agent_id IS NULL;

SET @fk_exists := (
    SELECT COUNT(*)
    FROM information_schema.table_constraints
    WHERE table_schema = DATABASE()
      AND table_name = 'monitors'
      AND constraint_name = 'fk_monitors_user_agent'
);
SET @ddl := IF(@fk_exists = 0,
    'ALTER TABLE monitors ADD CONSTRAINT fk_monitors_user_agent FOREIGN KEY (user_agent_id) REFERENCES user_agents(id) ON DELETE SET NULL',
    'DO 0');
PREPARE apply_ua_fk FROM @ddl;
EXECUTE apply_ua_fk;
DEALLOCATE PREPARE apply_ua_fk;
