-- Интервал повторной проверки после неуспешной попытки.
-- MySQL не поддерживает ADD COLUMN IF NOT EXISTS, поэтому проверяем вручную:
-- миграция должна переживать повторный запуск после сбоя на середине файла.
SET @col_exists := (
    SELECT COUNT(*)
    FROM information_schema.columns
    WHERE table_schema = DATABASE()
      AND table_name = 'monitors'
      AND column_name = 'retry_interval_seconds'
);
SET @ddl := IF(@col_exists = 0,
    'ALTER TABLE monitors ADD COLUMN retry_interval_seconds INT NOT NULL DEFAULT 10 AFTER interval_seconds',
    'DO 0');
PREPARE apply_retry_column FROM @ddl;
EXECUTE apply_retry_column;
DEALLOCATE PREPARE apply_retry_column;
