CREATE TABLE monitors (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    url VARCHAR(2048) NOT NULL,
    interval_seconds INT NOT NULL,
    expected_status INT NOT NULL DEFAULT 200,
    timeout_seconds INT NOT NULL DEFAULT 10,
    slow_threshold_ms INT NOT NULL,
    fail_threshold INT NOT NULL DEFAULT 3,
    enabled TINYINT(1) NOT NULL DEFAULT 1,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE checks (
    id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
    monitor_id BIGINT NOT NULL,
    checked_at DATETIME(3) NOT NULL,
    status_code INT NULL,
    response_ms INT NOT NULL,
    ok TINYINT(1) NOT NULL,
    slow TINYINT(1) NOT NULL,
    error_text TEXT NULL,
    INDEX idx_checks_monitor_checked (monitor_id, checked_at),
    INDEX idx_checks_checked_at (checked_at),
    CONSTRAINT fk_checks_monitor FOREIGN KEY (monitor_id) REFERENCES monitors(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE alert_states (
    monitor_id BIGINT NOT NULL PRIMARY KEY,
    consecutive_problems INT NOT NULL DEFAULT 0,
    down_alerted TINYINT(1) NOT NULL DEFAULT 0,
    slow_alerted TINYINT(1) NOT NULL DEFAULT 0,
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    CONSTRAINT fk_alert_monitor FOREIGN KEY (monitor_id) REFERENCES monitors(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
