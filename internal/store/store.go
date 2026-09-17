package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"webchecker/internal/models"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	db *sql.DB
}

func New(database *sql.DB) *Store {
	return &Store{db: database}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

func (s *Store) ListMonitors(ctx context.Context) ([]models.Monitor, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, url, interval_seconds, retry_interval_seconds, expected_status, timeout_seconds,
		       slow_threshold_ms, fail_threshold, enabled, created_at, updated_at
		FROM monitors
		ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list monitors: %w", err)
	}
	defer rows.Close()

	var out []models.Monitor
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) ListEnabledMonitors(ctx context.Context) ([]models.Monitor, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, url, interval_seconds, retry_interval_seconds, expected_status, timeout_seconds,
		       slow_threshold_ms, fail_threshold, enabled, created_at, updated_at
		FROM monitors
		WHERE enabled = 1
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list enabled monitors: %w", err)
	}
	defer rows.Close()

	var out []models.Monitor
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetMonitor(ctx context.Context, id int64) (models.Monitor, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, url, interval_seconds, retry_interval_seconds, expected_status, timeout_seconds,
		       slow_threshold_ms, fail_threshold, enabled, created_at, updated_at
		FROM monitors
		WHERE id = ?
	`, id)
	m, err := scanMonitor(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Monitor{}, ErrNotFound
	}
	if err != nil {
		return models.Monitor{}, fmt.Errorf("get monitor: %w", err)
	}
	return m, nil
}

func (s *Store) CreateMonitor(ctx context.Context, m models.Monitor) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO monitors (name, url, interval_seconds, retry_interval_seconds, expected_status, timeout_seconds,
		                      slow_threshold_ms, fail_threshold, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.Name, m.URL, m.IntervalSeconds, m.RetryIntervalSeconds, m.ExpectedStatus, m.TimeoutSeconds, m.SlowThresholdMS, m.FailThreshold, boolToInt(m.Enabled))
	if err != nil {
		return 0, fmt.Errorf("create monitor: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("create monitor id: %w", err)
	}
	return id, nil
}

func (s *Store) UpdateMonitor(ctx context.Context, m models.Monitor) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE monitors
		SET name = ?, url = ?, interval_seconds = ?, retry_interval_seconds = ?, expected_status = ?, timeout_seconds = ?,
		    slow_threshold_ms = ?, fail_threshold = ?, enabled = ?
		WHERE id = ?
	`, m.Name, m.URL, m.IntervalSeconds, m.RetryIntervalSeconds, m.ExpectedStatus, m.TimeoutSeconds, m.SlowThresholdMS, m.FailThreshold, boolToInt(m.Enabled), m.ID)
	if err != nil {
		return fmt.Errorf("update monitor: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	// MySQL reports 0 when SET values are identical to the current row.
	return s.requireMonitor(ctx, m.ID)
}

func (s *Store) requireMonitor(ctx context.Context, id int64) error {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM monitors WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get monitor: %w", err)
	}
	return nil
}

func (s *Store) SetMonitorEnabled(ctx context.Context, id int64, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE monitors SET enabled = ? WHERE id = ?`, boolToInt(enabled), id)
	if err != nil {
		return fmt.Errorf("toggle monitor: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteMonitor(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM monitors WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete monitor: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) InsertCheck(ctx context.Context, c models.Check) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO checks (monitor_id, checked_at, status_code, response_ms, ok, slow, error_text)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, c.MonitorID, c.CheckedAt, c.StatusCode, c.ResponseMS, boolToInt(c.OK), boolToInt(c.Slow), nullIfEmpty(c.ErrorText))
	if err != nil {
		return fmt.Errorf("insert check: %w", err)
	}
	return nil
}

func (s *Store) ListRecentChecks(ctx context.Context, monitorID int64, limit int) ([]models.Check, error) {
	if limit < 1 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, monitor_id, checked_at, status_code, response_ms, ok, slow, error_text
		FROM checks
		WHERE monitor_id = ?
		ORDER BY checked_at DESC, id DESC
		LIMIT ?
	`, monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("list checks: %w", err)
	}
	defer rows.Close()

	var out []models.Check
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Dashboard(ctx context.Context) ([]models.DashboardRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			m.id, m.name, m.url, m.interval_seconds, m.retry_interval_seconds, m.expected_status, m.timeout_seconds,
			m.slow_threshold_ms, m.fail_threshold, m.enabled, m.created_at, m.updated_at,
			c.status_code, c.response_ms, c.ok, c.slow, c.error_text, c.checked_at,
			s.uptime_24h
		FROM monitors m
		LEFT JOIN (
			SELECT ch.monitor_id, ch.status_code, ch.response_ms, ch.ok, ch.slow, ch.error_text, ch.checked_at
			FROM checks ch
			INNER JOIN (
				SELECT monitor_id, MAX(id) AS max_id
				FROM checks
				GROUP BY monitor_id
			) last ON last.max_id = ch.id
		) c ON c.monitor_id = m.id
		LEFT JOIN (
			SELECT monitor_id, 100.0 * SUM(ok) / COUNT(*) AS uptime_24h
			FROM checks
			WHERE checked_at >= UTC_TIMESTAMP(3) - INTERVAL 24 HOUR
			GROUP BY monitor_id
		) s ON s.monitor_id = m.id
		ORDER BY m.name
	`)
	if err != nil {
		return nil, fmt.Errorf("dashboard: %w", err)
	}
	defer rows.Close()

	var out []models.DashboardRow
	for rows.Next() {
		var row models.DashboardRow
		var enabled int
		var lastStatus sql.NullInt64
		var lastMS sql.NullInt64
		var lastOK sql.NullBool
		var lastSlow sql.NullBool
		var lastErr sql.NullString
		var lastAt sql.NullTime
		var uptime sql.NullFloat64

		err := rows.Scan(
			&row.ID, &row.Name, &row.URL, &row.IntervalSeconds, &row.RetryIntervalSeconds, &row.ExpectedStatus, &row.TimeoutSeconds,
			&row.SlowThresholdMS, &row.FailThreshold, &enabled, &row.CreatedAt, &row.UpdatedAt,
			&lastStatus, &lastMS, &lastOK, &lastSlow, &lastErr, &lastAt, &uptime,
		)
		if err != nil {
			return nil, fmt.Errorf("scan dashboard: %w", err)
		}
		row.Enabled = enabled == 1
		if lastStatus.Valid {
			code := int(lastStatus.Int64)
			row.LastStatusCode = &code
		}
		if lastMS.Valid {
			ms := int(lastMS.Int64)
			row.LastResponseMS = &ms
		}
		if lastOK.Valid {
			v := lastOK.Bool
			row.LastOK = &v
		}
		if lastSlow.Valid {
			v := lastSlow.Bool
			row.LastSlow = &v
		}
		if lastErr.Valid {
			row.LastError = lastErr.String
		}
		if lastAt.Valid {
			t := lastAt.Time.UTC()
			row.LastCheckedAt = &t
		}
		if uptime.Valid {
			v := uptime.Float64
			row.Uptime24h = &v
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) MonitorStats(ctx context.Context, monitorID int64) (models.MonitorStats, error) {
	var stats models.MonitorStats
	var err error
	stats.Last24h, err = s.periodStats(ctx, monitorID, 24*time.Hour)
	if err != nil {
		return models.MonitorStats{}, err
	}
	stats.Last7d, err = s.periodStats(ctx, monitorID, 7*24*time.Hour)
	if err != nil {
		return models.MonitorStats{}, err
	}
	return stats, nil
}

func (s *Store) LastCheck(ctx context.Context, monitorID int64) (*models.Check, error) {
	checks, err := s.ListRecentChecks(ctx, monitorID, 1)
	if err != nil {
		return nil, err
	}
	if len(checks) == 0 {
		return nil, nil
	}
	c := checks[0]
	return &c, nil
}

func (s *Store) periodStats(ctx context.Context, monitorID int64, window time.Duration) (models.PeriodStats, error) {
	hours := int(window.Hours())
	if hours < 1 {
		hours = 1
	}
	var total, okCount, slowCount, avg, minMS, maxMS sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(ok), 0),
			COALESCE(SUM(slow), 0),
			AVG(response_ms),
			MIN(response_ms),
			MAX(response_ms)
		FROM checks
		WHERE monitor_id = ? AND checked_at >= UTC_TIMESTAMP(3) - INTERVAL ? HOUR
	`, monitorID, hours).Scan(&total, &okCount, &slowCount, &avg, &minMS, &maxMS)
	if err != nil {
		return models.PeriodStats{}, fmt.Errorf("period stats: %w", err)
	}

	ps := models.PeriodStats{
		Total:     int(total.Float64),
		OKCount:   int(okCount.Float64),
		SlowCount: int(slowCount.Float64),
	}
	if ps.Total > 0 {
		ps.UptimePct = 100.0 * float64(ps.OKCount) / float64(ps.Total)
	}
	if avg.Valid {
		v := avg.Float64
		ps.AvgMS = &v
	}
	if minMS.Valid {
		v := int(minMS.Float64)
		ps.MinMS = &v
	}
	if maxMS.Valid {
		v := int(maxMS.Float64)
		ps.MaxMS = &v
	}
	return ps, nil
}

func (s *Store) GetOrCreateAlertState(ctx context.Context, monitorID int64) (models.AlertState, error) {
	state, err := s.getAlertState(ctx, monitorID)
	if err == nil {
		return state, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return models.AlertState{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO alert_states (monitor_id, consecutive_problems, down_alerted, slow_alerted)
		VALUES (?, 0, 0, 0)
	`, monitorID)
	if err != nil {
		if state, getErr := s.getAlertState(ctx, monitorID); getErr == nil {
			return state, nil
		}
		return models.AlertState{}, fmt.Errorf("create alert state: %w", err)
	}
	return s.getAlertState(ctx, monitorID)
}

func (s *Store) SaveAlertState(ctx context.Context, state models.AlertState) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO alert_states (monitor_id, consecutive_problems, down_alerted, slow_alerted)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			consecutive_problems = VALUES(consecutive_problems),
			down_alerted = VALUES(down_alerted),
			slow_alerted = VALUES(slow_alerted)
	`, state.MonitorID, state.ConsecutiveProblems, boolToInt(state.DownAlerted), boolToInt(state.SlowAlerted))
	if err != nil {
		return fmt.Errorf("save alert state: %w", err)
	}
	return nil
}

func (s *Store) DeleteOldChecks(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM checks WHERE checked_at < ?`, olderThan)
	if err != nil {
		return 0, fmt.Errorf("delete old checks: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) ExportDump(ctx context.Context) (models.Dump, error) {
	monitors, err := s.listMonitorsByID(ctx)
	if err != nil {
		return models.Dump{}, err
	}
	checks, err := s.listAllChecks(ctx)
	if err != nil {
		return models.Dump{}, err
	}
	alerts, err := s.listAlertStates(ctx)
	if err != nil {
		return models.Dump{}, err
	}
	if monitors == nil {
		monitors = []models.Monitor{}
	}
	if checks == nil {
		checks = []models.Check{}
	}
	if alerts == nil {
		alerts = []models.AlertState{}
	}
	return models.Dump{
		Version:     models.DumpVersion,
		ExportedAt:  time.Now().UTC(),
		Monitors:    monitors,
		Checks:      checks,
		AlertStates: alerts,
	}, nil
}

func (s *Store) ImportDump(ctx context.Context, dump models.Dump) error {
	if dump.Version != 0 && dump.Version != models.DumpVersion {
		return fmt.Errorf("неподдерживаемая версия дампа: %d", dump.Version)
	}
	monitorIDs := make(map[int64]struct{}, len(dump.Monitors))
	for _, m := range dump.Monitors {
		if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.URL) == "" {
			return fmt.Errorf("у каждого монитора должны быть имя и URL")
		}
		if m.ID > 0 {
			if _, ok := monitorIDs[m.ID]; ok {
				return fmt.Errorf("повторяющийся id монитора: %d", m.ID)
			}
			monitorIDs[m.ID] = struct{}{}
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM checks`); err != nil {
		return fmt.Errorf("clear checks: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM alert_states`); err != nil {
		return fmt.Errorf("clear alert states: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM monitors`); err != nil {
		return fmt.Errorf("clear monitors: %w", err)
	}

	idMap := make(map[int64]int64, len(dump.Monitors))
	var maxMonitorID int64
	for _, m := range dump.Monitors {
		newID, err := insertMonitorTx(ctx, tx, m)
		if err != nil {
			return err
		}
		if m.ID > 0 {
			idMap[m.ID] = newID
		}
		if newID > maxMonitorID {
			maxMonitorID = newID
		}
		monitorIDs[newID] = struct{}{}
	}

	var maxCheckID int64
	for _, c := range dump.Checks {
		monitorID := c.MonitorID
		if mapped, ok := idMap[c.MonitorID]; ok {
			monitorID = mapped
		}
		if _, ok := monitorIDs[monitorID]; !ok {
			return fmt.Errorf("проверка ссылается на неизвестный monitor_id %d", c.MonitorID)
		}
		c.MonitorID = monitorID
		if err := insertCheckTx(ctx, tx, c); err != nil {
			return err
		}
		if c.ID > maxCheckID {
			maxCheckID = c.ID
		}
	}

	for _, state := range dump.AlertStates {
		monitorID := state.MonitorID
		if mapped, ok := idMap[state.MonitorID]; ok {
			monitorID = mapped
		}
		if _, ok := monitorIDs[monitorID]; !ok {
			return fmt.Errorf("состояние алерта ссылается на неизвестный monitor_id %d", state.MonitorID)
		}
		state.MonitorID = monitorID
		if err := insertAlertStateTx(ctx, tx, state); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import: %w", err)
	}
	_ = resetAutoIncrement(ctx, s.db, "monitors", maxMonitorID)
	_ = resetAutoIncrement(ctx, s.db, "checks", maxCheckID)
	return nil
}

func (s *Store) listMonitorsByID(ctx context.Context) ([]models.Monitor, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, url, interval_seconds, retry_interval_seconds, expected_status, timeout_seconds,
		       slow_threshold_ms, fail_threshold, enabled, created_at, updated_at
		FROM monitors
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list monitors by id: %w", err)
	}
	defer rows.Close()

	var out []models.Monitor
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) listAllChecks(ctx context.Context) ([]models.Check, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, monitor_id, checked_at, status_code, response_ms, ok, slow, error_text
		FROM checks
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list all checks: %w", err)
	}
	defer rows.Close()

	var out []models.Check
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) listAlertStates(ctx context.Context) ([]models.AlertState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT monitor_id, consecutive_problems, down_alerted, slow_alerted, updated_at
		FROM alert_states
		ORDER BY monitor_id
	`)
	if err != nil {
		return nil, fmt.Errorf("list alert states: %w", err)
	}
	defer rows.Close()

	var out []models.AlertState
	for rows.Next() {
		var state models.AlertState
		var down, slow int
		if err := rows.Scan(&state.MonitorID, &state.ConsecutiveProblems, &down, &slow, &state.UpdatedAt); err != nil {
			return nil, err
		}
		state.DownAlerted = down == 1
		state.SlowAlerted = slow == 1
		out = append(out, state)
	}
	return out, rows.Err()
}

func insertMonitorTx(ctx context.Context, tx *sql.Tx, m models.Monitor) (int64, error) {
	if m.IntervalSeconds < 1 {
		m.IntervalSeconds = 60
	}
	if m.RetryIntervalSeconds < 1 {
		m.RetryIntervalSeconds = 10
	}
	if m.RetryIntervalSeconds > m.IntervalSeconds {
		m.RetryIntervalSeconds = m.IntervalSeconds
	}
	if m.ExpectedStatus < 1 {
		m.ExpectedStatus = 200
	}
	if m.TimeoutSeconds < 1 {
		m.TimeoutSeconds = 10
	}
	if m.SlowThresholdMS < 1 {
		m.SlowThresholdMS = 3000
	}
	if m.FailThreshold < 1 {
		m.FailThreshold = 3
	}
	created, updated := m.CreatedAt.UTC(), m.UpdatedAt.UTC()
	if created.IsZero() {
		created = time.Now().UTC()
	}
	if updated.IsZero() {
		updated = created
	}

	if m.ID > 0 {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO monitors (id, name, url, interval_seconds, retry_interval_seconds, expected_status, timeout_seconds,
			                      slow_threshold_ms, fail_threshold, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, m.ID, m.Name, m.URL, m.IntervalSeconds, m.RetryIntervalSeconds, m.ExpectedStatus, m.TimeoutSeconds, m.SlowThresholdMS, m.FailThreshold, boolToInt(m.Enabled), created, updated)
		if err != nil {
			return 0, fmt.Errorf("import monitor %d: %w", m.ID, err)
		}
		return m.ID, nil
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO monitors (name, url, interval_seconds, retry_interval_seconds, expected_status, timeout_seconds,
		                      slow_threshold_ms, fail_threshold, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.Name, m.URL, m.IntervalSeconds, m.RetryIntervalSeconds, m.ExpectedStatus, m.TimeoutSeconds, m.SlowThresholdMS, m.FailThreshold, boolToInt(m.Enabled), created, updated)
	if err != nil {
		return 0, fmt.Errorf("import monitor: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, nil
}

func insertCheckTx(ctx context.Context, tx *sql.Tx, c models.Check) error {
	checked := c.CheckedAt.UTC()
	if checked.IsZero() {
		checked = time.Now().UTC()
	}
	if c.ID > 0 {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO checks (id, monitor_id, checked_at, status_code, response_ms, ok, slow, error_text)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, c.ID, c.MonitorID, checked, c.StatusCode, c.ResponseMS, boolToInt(c.OK), boolToInt(c.Slow), nullIfEmpty(c.ErrorText))
		if err != nil {
			return fmt.Errorf("import check %d: %w", c.ID, err)
		}
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO checks (monitor_id, checked_at, status_code, response_ms, ok, slow, error_text)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, c.MonitorID, checked, c.StatusCode, c.ResponseMS, boolToInt(c.OK), boolToInt(c.Slow), nullIfEmpty(c.ErrorText))
	if err != nil {
		return fmt.Errorf("import check: %w", err)
	}
	return nil
}

func insertAlertStateTx(ctx context.Context, tx *sql.Tx, state models.AlertState) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO alert_states (monitor_id, consecutive_problems, down_alerted, slow_alerted)
		VALUES (?, ?, ?, ?)
	`, state.MonitorID, state.ConsecutiveProblems, boolToInt(state.DownAlerted), boolToInt(state.SlowAlerted))
	if err != nil {
		return fmt.Errorf("import alert state %d: %w", state.MonitorID, err)
	}
	return nil
}

func resetAutoIncrement(ctx context.Context, exec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}, table string, maxID int64) error {
	if table != "monitors" && table != "checks" {
		return fmt.Errorf("unknown table %s", table)
	}
	if maxID < 1 {
		maxID = 1
	}
	query := fmt.Sprintf("ALTER TABLE %s AUTO_INCREMENT = %d", table, maxID+1)
	if _, err := exec.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("reset auto_increment %s: %w", table, err)
	}
	return nil
}

func (s *Store) getAlertState(ctx context.Context, monitorID int64) (models.AlertState, error) {
	var state models.AlertState
	var down, slow int
	err := s.db.QueryRowContext(ctx, `
		SELECT monitor_id, consecutive_problems, down_alerted, slow_alerted, updated_at
		FROM alert_states
		WHERE monitor_id = ?
	`, monitorID).Scan(&state.MonitorID, &state.ConsecutiveProblems, &down, &slow, &state.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.AlertState{}, ErrNotFound
	}
	if err != nil {
		return models.AlertState{}, fmt.Errorf("get alert state: %w", err)
	}
	state.DownAlerted = down == 1
	state.SlowAlerted = slow == 1
	return state, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanMonitor(sc scanner) (models.Monitor, error) {
	var m models.Monitor
	var enabled int
	err := sc.Scan(
		&m.ID, &m.Name, &m.URL, &m.IntervalSeconds, &m.RetryIntervalSeconds, &m.ExpectedStatus, &m.TimeoutSeconds,
		&m.SlowThresholdMS, &m.FailThreshold, &enabled, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return models.Monitor{}, err
	}
	m.Enabled = enabled == 1
	m.CreatedAt = m.CreatedAt.UTC()
	m.UpdatedAt = m.UpdatedAt.UTC()
	return m, nil
}

func scanCheck(sc scanner) (models.Check, error) {
	var c models.Check
	var status sql.NullInt64
	var ok, slow int
	var errText sql.NullString
	err := sc.Scan(&c.ID, &c.MonitorID, &c.CheckedAt, &status, &c.ResponseMS, &ok, &slow, &errText)
	if err != nil {
		return models.Check{}, err
	}
	c.CheckedAt = c.CheckedAt.UTC()
	if status.Valid {
		code := int(status.Int64)
		c.StatusCode = &code
	}
	c.OK = ok == 1
	c.Slow = slow == 1
	if errText.Valid {
		c.ErrorText = errText.String
	}
	return c, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
