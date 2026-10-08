package store

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"webchecker/internal/models"
)

var ErrNotFound = errors.New("not found")
var ErrLastUserAgent = errors.New("cannot delete the last user agent")

const monitorSelect = `m.id, m.name, m.url, m.interval_seconds, m.retry_interval_seconds, m.expected_status, m.timeout_seconds,
		       m.slow_threshold_ms, m.fail_threshold, m.enabled, m.created_at, m.updated_at, m.user_agent_id,
		       COALESCE(ua.value, ''), COALESCE(ua.name, '')`
const monitorFrom = `monitors m LEFT JOIN user_agents ua ON ua.id = m.user_agent_id`

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
		SELECT `+monitorSelect+`
		FROM `+monitorFrom+`
		ORDER BY m.name
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
		SELECT `+monitorSelect+`
		FROM `+monitorFrom+`
		WHERE m.enabled = 1
		ORDER BY m.id
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
		SELECT `+monitorSelect+`
		FROM `+monitorFrom+`
		WHERE m.id = ?
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
	uaID, err := s.resolveUserAgentID(ctx, m.UserAgentID)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO monitors (name, url, interval_seconds, retry_interval_seconds, expected_status, timeout_seconds,
		                      slow_threshold_ms, fail_threshold, user_agent_id, enabled)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.Name, m.URL, m.IntervalSeconds, m.RetryIntervalSeconds, m.ExpectedStatus, m.TimeoutSeconds, m.SlowThresholdMS, m.FailThreshold, uaID, boolToInt(m.Enabled))
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
	uaID, err := s.resolveUserAgentID(ctx, m.UserAgentID)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE monitors
		SET name = ?, url = ?, interval_seconds = ?, retry_interval_seconds = ?, expected_status = ?, timeout_seconds = ?,
		    slow_threshold_ms = ?, fail_threshold = ?, user_agent_id = ?, enabled = ?
		WHERE id = ?
	`, m.Name, m.URL, m.IntervalSeconds, m.RetryIntervalSeconds, m.ExpectedStatus, m.TimeoutSeconds, m.SlowThresholdMS, m.FailThreshold, uaID, boolToInt(m.Enabled), m.ID)
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

// ToggleMonitorEnabled переключает флаг одним запросом, без чтения текущего
// состояния: read-then-write мог разъехаться при двух кликах подряд.
func (s *Store) ToggleMonitorEnabled(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE monitors SET enabled = 1 - enabled WHERE id = ?`, id)
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

func (s *Store) SetMonitorEnabled(ctx context.Context, id int64, enabled bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE monitors SET enabled = ? WHERE id = ?`, boolToInt(enabled), id)
	if err != nil {
		return fmt.Errorf("set monitor enabled: %w", err)
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

// LastChecks возвращает последнюю проверку для каждого из monitorIDs одним
// запросом. Мониторы без истории в карте отсутствуют.
func (s *Store) LastChecks(ctx context.Context, monitorIDs []int64) (map[int64]*models.Check, error) {
	out := make(map[int64]*models.Check, len(monitorIDs))
	if len(monitorIDs) == 0 {
		return out, nil
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(monitorIDs)), ",")
	args := make([]any, 0, len(monitorIDs)*2)
	for _, id := range monitorIDs {
		args = append(args, id)
	}
	args = append(args, args[:len(monitorIDs)]...)

	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.monitor_id, c.checked_at, c.status_code, c.response_ms, c.ok, c.slow, c.error_text
		FROM checks c
		INNER JOIN (
			SELECT monitor_id, MAX(id) AS max_id
			FROM checks
			WHERE monitor_id IN (`+placeholders+`)
			GROUP BY monitor_id
		) last ON last.max_id = c.id
		WHERE c.monitor_id IN (`+placeholders+`)
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("last checks: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		last := c
		out[c.MonitorID] = &last
	}
	return out, rows.Err()
}

// PeriodStats считает агрегаты за последние hours часов.
func (s *Store) PeriodStats(ctx context.Context, monitorID int64, hours int) (models.PeriodStats, error) {
	if hours < 1 {
		hours = 1
	}
	return s.periodStats(ctx, monitorID, time.Duration(hours)*time.Hour)
}

// ListCheckBuckets сжимает проверки в корзины по bucketSec секунд.
// Пустые корзины не возвращаются.
func (s *Store) ListCheckBuckets(ctx context.Context, monitorID int64, hours, bucketSec int) ([]models.CheckBucket, error) {
	if hours < 1 {
		hours = 1
	}
	if bucketSec < 1 {
		bucketSec = 60
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			FROM_UNIXTIME(FLOOR(UNIX_TIMESTAMP(checked_at) / ?) * ?) AS bucket,
			AVG(response_ms),
			COALESCE(SUM(ok), 0),
			COUNT(*)
		FROM checks
		WHERE monitor_id = ? AND checked_at >= UTC_TIMESTAMP(3) - INTERVAL ? HOUR
		GROUP BY bucket
		ORDER BY bucket
	`, bucketSec, bucketSec, monitorID, hours)
	if err != nil {
		return nil, fmt.Errorf("list check buckets: %w", err)
	}
	defer rows.Close()

	var out []models.CheckBucket
	for rows.Next() {
		var at time.Time
		var avg sql.NullFloat64
		var okCount, total sql.NullInt64
		if err := rows.Scan(&at, &avg, &okCount, &total); err != nil {
			return nil, fmt.Errorf("scan check bucket: %w", err)
		}
		b := models.CheckBucket{
			At:    at.UTC(),
			Count: int(total.Int64),
			OK:    total.Valid && okCount.Valid && okCount.Int64 == total.Int64 && total.Int64 > 0,
		}
		if avg.Valid {
			b.AvgMS = int(avg.Float64 + 0.5)
		}
		out = append(out, b)
	}
	return out, rows.Err()
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

const deleteChecksBatch = 5000

// DeleteOldChecks удаляет историю батчами: одиночный DELETE на миллионах строк
// держит долгую блокировку и раздувает undo log.
func (s *Store) DeleteOldChecks(ctx context.Context, olderThan time.Time) (int64, error) {
	var total int64
	for {
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM checks WHERE checked_at < ? LIMIT ?`, olderThan, deleteChecksBatch)
		if err != nil {
			return total, fmt.Errorf("delete old checks: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n < deleteChecksBatch {
			return total, nil
		}
		if err := ctx.Err(); err != nil {
			return total, err
		}
	}
}

// StreamDump пишет дамп прямо в w, не собирая историю в памяти: при 30 днях
// хранения таблица checks — это миллионы строк.
func (s *Store) StreamDump(ctx context.Context, w io.Writer, includeChecks bool) error {
	bw := bufio.NewWriterSize(w, 64<<10)
	enc := json.NewEncoder(bw)

	writeRaw := func(s string) {
		_, _ = bw.WriteString(s)
	}

	writeRaw(`{"version":`)
	if err := enc.Encode(models.DumpVersion); err != nil {
		return err
	}
	writeRaw(`,"exported_at":`)
	if err := enc.Encode(time.Now().UTC()); err != nil {
		return err
	}

	writeRaw(`,"user_agents":[`)
	if err := s.streamUserAgents(ctx, bw, enc); err != nil {
		return err
	}

	writeRaw(`],"monitors":[`)
	if err := s.streamMonitors(ctx, bw, enc); err != nil {
		return err
	}

	writeRaw(`],"checks":[`)
	if includeChecks {
		if err := s.streamChecks(ctx, bw, enc); err != nil {
			return err
		}
	}

	writeRaw(`],"alert_states":[`)
	if err := s.streamAlertStates(ctx, bw, enc); err != nil {
		return err
	}
	writeRaw("]}\n")

	return bw.Flush()
}

func (s *Store) streamMonitors(ctx context.Context, bw *bufio.Writer, enc *json.Encoder) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+monitorSelect+`
		FROM `+monitorFrom+`
		ORDER BY m.id
	`)
	if err != nil {
		return fmt.Errorf("export monitors: %w", err)
	}
	defer rows.Close()

	first := true
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return err
		}
		if !first {
			_, _ = bw.WriteString(",")
		}
		first = false
		if err := enc.Encode(m); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) streamChecks(ctx context.Context, bw *bufio.Writer, enc *json.Encoder) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, monitor_id, checked_at, status_code, response_ms, ok, slow, error_text
		FROM checks
		ORDER BY id
	`)
	if err != nil {
		return fmt.Errorf("export checks: %w", err)
	}
	defer rows.Close()

	first := true
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return err
		}
		if !first {
			_, _ = bw.WriteString(",")
		}
		first = false
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) streamAlertStates(ctx context.Context, bw *bufio.Writer, enc *json.Encoder) error {
	states, err := s.listAlertStates(ctx)
	if err != nil {
		return err
	}
	for i, state := range states {
		if i > 0 {
			_, _ = bw.WriteString(",")
		}
		if err := enc.Encode(state); err != nil {
			return err
		}
	}
	return nil
}

// ImportStream читает дамп потоково, элемент за элементом: файл с историей
// может быть в сотни мегабайт, и держать его в памяти целиком нельзя.
// Нужен ReadSeeker, потому что мониторы должны попасть в БД раньше проверок,
// а порядок ключей в чужом JSON не гарантирован.
func (s *Store) ImportStream(ctx context.Context, src io.ReadSeeker) error {
	version, err := readDumpVersion(src)
	if err != nil {
		return err
	}
	if version != 0 && version != models.DumpVersion {
		return fmt.Errorf("неподдерживаемая версия дампа: %d", version)
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

	uaIDs := make(map[int64]struct{})
	importedUA := false
	var maxUAID int64

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind dump: %w", err)
	}
	err = walkDumpObject(src, map[string]dumpHandler{
		"user_agents": func(dec *json.Decoder) error {
			importedUA = true
			if _, err := tx.ExecContext(ctx, `DELETE FROM user_agents`); err != nil {
				return fmt.Errorf("clear user agents: %w", err)
			}
			return eachArrayElement(dec, func() error {
				var ua models.UserAgent
				if err := dec.Decode(&ua); err != nil {
					return errInvalidDump
				}
				newID, err := insertUserAgentTx(ctx, tx, ua)
				if err != nil {
					return err
				}
				uaIDs[newID] = struct{}{}
				if newID > maxUAID {
					maxUAID = newID
				}
				return nil
			})
		},
	})
	if err != nil {
		return err
	}
	if !importedUA || len(uaIDs) == 0 {
		if err := seedDefaultUserAgentsTx(ctx, tx); err != nil {
			return err
		}
		if err := loadUserAgentIDsTx(ctx, tx, uaIDs); err != nil {
			return err
		}
	}

	idMap := make(map[int64]int64)
	monitorIDs := make(map[int64]struct{})
	dumpIDs := make(map[int64]struct{})
	var maxMonitorID int64

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind dump: %w", err)
	}
	err = walkDumpObject(src, map[string]dumpHandler{
		"monitors": func(dec *json.Decoder) error {
			return eachArrayElement(dec, func() error {
				var m models.Monitor
				if err := dec.Decode(&m); err != nil {
					return errInvalidDump
				}
				if strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.URL) == "" {
					return fmt.Errorf("у каждого монитора должны быть имя и URL")
				}
				if m.UserAgentID != 0 {
					if _, ok := uaIDs[m.UserAgentID]; !ok {
						m.UserAgentID = 0
					}
				}
				if m.ID > 0 {
					if _, dup := dumpIDs[m.ID]; dup {
						return fmt.Errorf("повторяющийся id монитора: %d", m.ID)
					}
					dumpIDs[m.ID] = struct{}{}
				}
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
				return nil
			})
		},
	})
	if err != nil {
		return err
	}

	resolve := func(id int64) (int64, bool) {
		if mapped, ok := idMap[id]; ok {
			return mapped, true
		}
		if _, ok := monitorIDs[id]; ok {
			return id, true
		}
		return 0, false
	}

	checks := newCheckBatch(tx)
	var maxCheckID int64

	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind dump: %w", err)
	}
	err = walkDumpObject(src, map[string]dumpHandler{
		"checks": func(dec *json.Decoder) error {
			return eachArrayElement(dec, func() error {
				var c models.Check
				if err := dec.Decode(&c); err != nil {
					return errInvalidDump
				}
				monitorID, ok := resolve(c.MonitorID)
				if !ok {
					return fmt.Errorf("проверка ссылается на неизвестный monitor_id %d", c.MonitorID)
				}
				c.MonitorID = monitorID
				if c.ID > maxCheckID {
					maxCheckID = c.ID
				}
				return checks.add(ctx, c)
			})
		},
		"alert_states": func(dec *json.Decoder) error {
			return eachArrayElement(dec, func() error {
				var state models.AlertState
				if err := dec.Decode(&state); err != nil {
					return errInvalidDump
				}
				monitorID, ok := resolve(state.MonitorID)
				if !ok {
					return fmt.Errorf("состояние алерта ссылается на неизвестный monitor_id %d", state.MonitorID)
				}
				state.MonitorID = monitorID
				return insertAlertStateTx(ctx, tx, state)
			})
		},
	})
	if err != nil {
		return err
	}
	if err := checks.flush(ctx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import: %w", err)
	}
	_ = resetAutoIncrement(ctx, s.db, "monitors", maxMonitorID)
	_ = resetAutoIncrement(ctx, s.db, "checks", maxCheckID)
	if importedUA {
		_ = resetAutoIncrement(ctx, s.db, "user_agents", maxUAID)
	}
	return nil
}

var errInvalidDump = errors.New("файл не является корректным JSON-дампом webChecker")

type dumpHandler func(*json.Decoder) error

// walkDumpObject обходит объект верхнего уровня, вызывая обработчик для
// известных ключей и пропуская остальные значения без буферизации.
func walkDumpObject(src io.Reader, handlers map[string]dumpHandler) error {
	dec := json.NewDecoder(src)
	if err := expectDelim(dec, '{'); err != nil {
		return err
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return errInvalidDump
		}
		key, ok := tok.(string)
		if !ok {
			return errInvalidDump
		}
		if handler, known := handlers[key]; known {
			if err := handler(dec); err != nil {
				return err
			}
			continue
		}
		if err := skipValue(dec); err != nil {
			return err
		}
	}
	return expectDelim(dec, '}')
}

func readDumpVersion(src io.ReadSeeker) (int, error) {
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("rewind dump: %w", err)
	}
	version := 0
	err := walkDumpObject(src, map[string]dumpHandler{
		"version": func(dec *json.Decoder) error {
			if err := dec.Decode(&version); err != nil {
				return errInvalidDump
			}
			return nil
		},
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}

func eachArrayElement(dec *json.Decoder, decode func() error) error {
	if err := expectDelim(dec, '['); err != nil {
		return err
	}
	for dec.More() {
		if err := decode(); err != nil {
			return err
		}
	}
	return expectDelim(dec, ']')
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return errInvalidDump
	}
	got, ok := tok.(json.Delim)
	if !ok || got != want {
		return errInvalidDump
	}
	return nil
}

// skipValue проматывает значение любой вложенности по токенам, не собирая его
// в памяти: в первом проходе так пропускается массив checks.
func skipValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return errInvalidDump
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '[' && delim != '{' {
		return errInvalidDump
	}
	for dec.More() {
		if err := skipValue(dec); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return errInvalidDump
	}
	return nil
}

const checkBatchSize = 500

// checkBatch вставляет проверки пачками: построчный INSERT на миллионе строк
// превращает импорт в часы ожидания.
type checkBatch struct {
	tx   *sql.Tx
	args []any
	rows int
}

func newCheckBatch(tx *sql.Tx) *checkBatch {
	return &checkBatch{tx: tx, args: make([]any, 0, checkBatchSize*8)}
}

func (b *checkBatch) add(ctx context.Context, c models.Check) error {
	checked := c.CheckedAt.UTC()
	if checked.IsZero() {
		checked = time.Now().UTC()
	}
	// NULL в AUTO_INCREMENT означает "назначь сам", поэтому дампы с id и без
	// него попадают в одну пачку.
	var id any
	if c.ID > 0 {
		id = c.ID
	}
	b.args = append(b.args, id, c.MonitorID, checked, c.StatusCode, c.ResponseMS,
		boolToInt(c.OK), boolToInt(c.Slow), nullIfEmpty(c.ErrorText))
	b.rows++
	if b.rows >= checkBatchSize {
		return b.flush(ctx)
	}
	return nil
}

func (b *checkBatch) flush(ctx context.Context) error {
	if b.rows == 0 {
		return nil
	}
	values := strings.TrimSuffix(strings.Repeat("(?, ?, ?, ?, ?, ?, ?, ?),", b.rows), ",")
	_, err := b.tx.ExecContext(ctx, `
		INSERT INTO checks (id, monitor_id, checked_at, status_code, response_ms, ok, slow, error_text)
		VALUES `+values, b.args...)
	if err != nil {
		return fmt.Errorf("import checks: %w", err)
	}
	b.args = b.args[:0]
	b.rows = 0
	return nil
}

func (s *Store) listMonitorsByID(ctx context.Context) ([]models.Monitor, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+monitorSelect+`
		FROM `+monitorFrom+`
		ORDER BY m.id
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
	if m.UserAgentID == 0 {
		var def sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM user_agents WHERE is_default = 1 ORDER BY id LIMIT 1`).Scan(&def); err == nil && def.Valid {
			m.UserAgentID = def.Int64
		}
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
			                      slow_threshold_ms, fail_threshold, user_agent_id, enabled, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, m.ID, m.Name, m.URL, m.IntervalSeconds, m.RetryIntervalSeconds, m.ExpectedStatus, m.TimeoutSeconds, m.SlowThresholdMS, m.FailThreshold, nullIfZero(m.UserAgentID), boolToInt(m.Enabled), created, updated)
		if err != nil {
			return 0, fmt.Errorf("import monitor %d: %w", m.ID, err)
		}
		return m.ID, nil
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO monitors (name, url, interval_seconds, retry_interval_seconds, expected_status, timeout_seconds,
		                      slow_threshold_ms, fail_threshold, user_agent_id, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.Name, m.URL, m.IntervalSeconds, m.RetryIntervalSeconds, m.ExpectedStatus, m.TimeoutSeconds, m.SlowThresholdMS, m.FailThreshold, nullIfZero(m.UserAgentID), boolToInt(m.Enabled), created, updated)
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
	if table != "monitors" && table != "checks" && table != "user_agents" {
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
	var uaID sql.NullInt64
	err := sc.Scan(
		&m.ID, &m.Name, &m.URL, &m.IntervalSeconds, &m.RetryIntervalSeconds, &m.ExpectedStatus, &m.TimeoutSeconds,
		&m.SlowThresholdMS, &m.FailThreshold, &enabled, &m.CreatedAt, &m.UpdatedAt, &uaID, &m.UserAgent, &m.UserAgentName,
	)
	if err != nil {
		return models.Monitor{}, err
	}
	m.Enabled = enabled == 1
	if uaID.Valid {
		m.UserAgentID = uaID.Int64
	}
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

func nullIfZero(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
