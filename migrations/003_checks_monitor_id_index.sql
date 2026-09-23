-- Индекс под запрос дашборда "последняя проверка каждого монитора":
-- SELECT monitor_id, MAX(id) FROM checks GROUP BY monitor_id.
-- Без него MySQL читает всю таблицу checks на каждую загрузку страницы.
SET @idx_exists := (
    SELECT COUNT(*)
    FROM information_schema.statistics
    WHERE table_schema = DATABASE()
      AND table_name = 'checks'
      AND index_name = 'idx_checks_monitor_id'
);
SET @ddl := IF(@idx_exists = 0,
    'CREATE INDEX idx_checks_monitor_id ON checks (monitor_id, id)',
    'DO 0');
PREPARE apply_checks_index FROM @ddl;
EXECUTE apply_checks_index;
DEALLOCATE PREPARE apply_checks_index;
