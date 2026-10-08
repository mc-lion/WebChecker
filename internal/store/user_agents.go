package store

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"webchecker/internal/models"
)

const (
	maxUserAgentName  = 100
	maxUserAgentValue = 512
)

var defaultUserAgents = []models.UserAgent{
	{Name: "Chrome (Windows)", Value: models.DefaultBrowserUA, IsDefault: true, SortOrder: 1},
	{Name: "Firefox (Windows)", Value: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:133.0) Gecko/20100101 Firefox/133.0", SortOrder: 2},
	{Name: "Safari (macOS)", Value: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15", SortOrder: 3},
	{Name: "Chrome (Android)", Value: "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Mobile Safari/537.36", SortOrder: 4},
	{Name: "webChecker", Value: "webChecker/1.0", SortOrder: 5},
}

func (s *Store) ListUserAgents(ctx context.Context) ([]models.UserAgent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, value, is_default, sort_order, created_at
		FROM user_agents
		ORDER BY sort_order, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list user agents: %w", err)
	}
	defer rows.Close()

	var out []models.UserAgent
	for rows.Next() {
		ua, err := scanUserAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ua)
	}
	return out, rows.Err()
}

func (s *Store) CreateUserAgent(ctx context.Context, ua models.UserAgent) (int64, error) {
	ua.Name = strings.TrimSpace(ua.Name)
	ua.Value = strings.TrimSpace(ua.Value)
	if err := validateUserAgent(ua); err != nil {
		return 0, err
	}
	if ua.SortOrder < 1 {
		_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(sort_order), 0) + 1 FROM user_agents`).Scan(&ua.SortOrder)
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO user_agents (name, value, is_default, sort_order)
		VALUES (?, ?, 0, ?)
	`, ua.Name, ua.Value, ua.SortOrder)
	if err != nil {
		return 0, fmt.Errorf("create user agent: %w", err)
	}
	return res.LastInsertId()
}

func (s *Store) UpdateUserAgent(ctx context.Context, ua models.UserAgent) error {
	ua.Name = strings.TrimSpace(ua.Name)
	ua.Value = strings.TrimSpace(ua.Value)
	if err := validateUserAgent(ua); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE user_agents SET name = ?, value = ? WHERE id = ?
	`, ua.Name, ua.Value, ua.ID)
	if err != nil {
		return fmt.Errorf("update user agent: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		var one int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM user_agents WHERE id = ?`, ua.ID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

func (s *Store) DeleteUserAgent(ctx context.Context, id int64) error {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_agents`).Scan(&total); err != nil {
		return fmt.Errorf("count user agents: %w", err)
	}
	if total <= 1 {
		return ErrLastUserAgent
	}
	var isDefault int
	err := s.db.QueryRowContext(ctx, `SELECT is_default FROM user_agents WHERE id = ?`, id).Scan(&isDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("get user agent: %w", err)
	}
	wasDefault := isDefault == 1
	if _, err := s.db.ExecContext(ctx, `DELETE FROM user_agents WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete user agent: %w", err)
	}
	if wasDefault {
		_, _ = s.db.ExecContext(ctx, `
			UPDATE user_agents SET is_default = 1
			WHERE id = (SELECT id FROM (SELECT id FROM user_agents ORDER BY sort_order, id LIMIT 1) t)
		`)
	}
	_, _ = s.db.ExecContext(ctx, `
		UPDATE monitors
		SET user_agent_id = (SELECT id FROM user_agents WHERE is_default = 1 ORDER BY id LIMIT 1)
		WHERE user_agent_id IS NULL
	`)
	return nil
}

func (s *Store) SetDefaultUserAgent(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM user_agents WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_agents SET is_default = 0`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE user_agents SET is_default = 1 WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) resolveUserAgentID(ctx context.Context, id int64) (any, error) {
	if id > 0 {
		var one int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM user_agents WHERE id = ?`, id).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			id = 0
		} else if err != nil {
			return nil, fmt.Errorf("resolve user agent: %w", err)
		} else {
			return id, nil
		}
	}
	var def sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM user_agents WHERE is_default = 1 ORDER BY id LIMIT 1`).Scan(&def)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("default user agent: %w", err)
	}
	if def.Valid {
		return def.Int64, nil
	}
	return nil, nil
}

func (s *Store) streamUserAgents(ctx context.Context, bw *bufio.Writer, enc *json.Encoder) error {
	agents, err := s.ListUserAgents(ctx)
	if err != nil {
		return err
	}
	for i, ua := range agents {
		if i > 0 {
			_, _ = bw.WriteString(",")
		}
		if err := enc.Encode(ua); err != nil {
			return err
		}
	}
	return nil
}

func insertUserAgentTx(ctx context.Context, tx *sql.Tx, ua models.UserAgent) (int64, error) {
	ua.Name = strings.TrimSpace(ua.Name)
	ua.Value = strings.TrimSpace(ua.Value)
	if err := validateUserAgent(ua); err != nil {
		return 0, err
	}
	if ua.ID > 0 {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO user_agents (id, name, value, is_default, sort_order)
			VALUES (?, ?, ?, ?, ?)
		`, ua.ID, ua.Name, ua.Value, boolToInt(ua.IsDefault), ua.SortOrder)
		if err != nil {
			return 0, fmt.Errorf("import user agent %d: %w", ua.ID, err)
		}
		return ua.ID, nil
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO user_agents (name, value, is_default, sort_order)
		VALUES (?, ?, ?, ?)
	`, ua.Name, ua.Value, boolToInt(ua.IsDefault), ua.SortOrder)
	if err != nil {
		return 0, fmt.Errorf("import user agent: %w", err)
	}
	return res.LastInsertId()
}

func seedDefaultUserAgentsTx(ctx context.Context, tx *sql.Tx) error {
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_agents`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for _, ua := range defaultUserAgents {
		if _, err := insertUserAgentTx(ctx, tx, ua); err != nil {
			return err
		}
	}
	return nil
}

func loadUserAgentIDsTx(ctx context.Context, tx *sql.Tx, ids map[int64]struct{}) error {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM user_agents`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids[id] = struct{}{}
	}
	return rows.Err()
}

func scanUserAgent(sc scanner) (models.UserAgent, error) {
	var ua models.UserAgent
	var def int
	if err := sc.Scan(&ua.ID, &ua.Name, &ua.Value, &def, &ua.SortOrder, &ua.CreatedAt); err != nil {
		return models.UserAgent{}, err
	}
	ua.IsDefault = def == 1
	return ua, nil
}

func validateUserAgent(ua models.UserAgent) error {
	ua.Name = strings.TrimSpace(ua.Name)
	ua.Value = strings.TrimSpace(ua.Value)
	if ua.Name == "" {
		return fmt.Errorf("укажите название User-Agent")
	}
	if ua.Value == "" {
		return fmt.Errorf("укажите строку User-Agent")
	}
	if len(ua.Name) > maxUserAgentName {
		return fmt.Errorf("название User-Agent слишком длинное")
	}
	if len(ua.Value) > maxUserAgentValue {
		return fmt.Errorf("строка User-Agent слишком длинная")
	}
	return nil
}
