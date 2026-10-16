package store

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"golang.org/x/crypto/bcrypt"

	"webchecker/internal/models"
)

const (
	maxUsernameLen = 64
	minPasswordLen = 8
)

var (
	ErrLastEnabledUser   = errors.New("cannot remove or disable the last enabled user")
	ErrDuplicateUsername = errors.New("username already exists")
	ErrWeakPassword      = errors.New("password too short")
	ErrInvalidUsername   = errors.New("invalid username")
)

func (s *Store) ListUsers(ctx context.Context) ([]models.User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, username, password_hash, enabled, created_at, updated_at
		FROM users
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var out []models.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) GetUser(ctx context.Context, id int64) (models.User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, enabled, created_at, updated_at
		FROM users WHERE id = ?
	`, id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.User{}, ErrNotFound
	}
	if err != nil {
		return models.User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (models.User, error) {
	username = strings.TrimSpace(username)
	row := s.db.QueryRowContext(ctx, `
		SELECT id, username, password_hash, enabled, created_at, updated_at
		FROM users WHERE username = ?
	`, username)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return models.User{}, ErrNotFound
	}
	if err != nil {
		return models.User{}, fmt.Errorf("get user by username: %w", err)
	}
	return u, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

func (s *Store) CountEnabledUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE enabled = 1`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count enabled users: %w", err)
	}
	return n, nil
}

// Authenticate проверяет логин/пароль пользователя из БД.
// ErrNotFound — пользователь не найден; иначе ошибка сравнения или disabled.
func (s *Store) Authenticate(ctx context.Context, username, password string) (models.User, error) {
	u, err := s.GetUserByUsername(ctx, username)
	if err != nil {
		return models.User{}, err
	}
	if !u.Enabled {
		return models.User{}, ErrNotFound
	}
	if err := ComparePassword(u.PasswordHash, password); err != nil {
		return models.User{}, ErrNotFound
	}
	return u, nil
}

// ComparePassword сравнивает bcrypt-хеш с паролем. Не nil — не совпало.
func ComparePassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

func (s *Store) CreateUser(ctx context.Context, username, password string, enabled bool) (int64, error) {
	username = strings.TrimSpace(username)
	if err := validateUsername(username); err != nil {
		return 0, err
	}
	if len(password) < minPasswordLen {
		return 0, ErrWeakPassword
	}
	hash, err := hashPassword(password)
	if err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, enabled)
		VALUES (?, ?, ?)
	`, username, hash, enabled)
	if err != nil {
		return 0, mapUserWriteErr("create user", err)
	}
	return res.LastInsertId()
}

// UpdateUser обновляет username/enabled; password пустой — не менять хеш.
func (s *Store) UpdateUser(ctx context.Context, id int64, username, password string, enabled bool) error {
	username = strings.TrimSpace(username)
	if err := validateUsername(username); err != nil {
		return err
	}
	if password != "" && len(password) < minPasswordLen {
		return ErrWeakPassword
	}

	cur, err := s.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if cur.Enabled && !enabled {
		n, err := s.CountEnabledUsers(ctx)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastEnabledUser
		}
	}

	if password != "" {
		hash, err := hashPassword(password)
		if err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx, `
			UPDATE users SET username = ?, password_hash = ?, enabled = ? WHERE id = ?
		`, username, hash, enabled, id)
		return mapUserWriteErr("update user", err)
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE users SET username = ?, enabled = ? WHERE id = ?
	`, username, enabled, id)
	return mapUserWriteErr("update user", err)
}

func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	cur, err := s.GetUser(ctx, id)
	if err != nil {
		return err
	}
	if cur.Enabled {
		n, err := s.CountEnabledUsers(ctx)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastEnabledUser
		}
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
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

func hashPassword(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(b), nil
}

func validateUsername(username string) error {
	if username == "" || utf8.RuneCountInString(username) > maxUsernameLen {
		return ErrInvalidUsername
	}
	if strings.ContainsAny(username, " \t\r\n") {
		return ErrInvalidUsername
	}
	return nil
}

func mapUserWriteErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		return ErrDuplicateUsername
	}
	return fmt.Errorf("%s: %w", op, err)
}

type userScanner interface {
	Scan(dest ...any) error
}

func scanUser(row userScanner) (models.User, error) {
	var u models.User
	var enabled int
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &enabled, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return models.User{}, err
	}
	u.Enabled = enabled == 1
	return u, nil
}

func (s *Store) streamUsers(ctx context.Context, bw *bufio.Writer, enc *json.Encoder) error {
	users, err := s.ListUsers(ctx)
	if err != nil {
		return err
	}
	for i, u := range users {
		if i > 0 {
			_, _ = bw.WriteString(",")
		}
		if err := enc.Encode(u); err != nil {
			return err
		}
	}
	return nil
}

func insertUserTx(ctx context.Context, tx *sql.Tx, u models.User) (int64, error) {
	u.Username = strings.TrimSpace(u.Username)
	if err := validateUsername(u.Username); err != nil {
		return 0, err
	}
	if strings.TrimSpace(u.PasswordHash) == "" {
		return 0, fmt.Errorf("password_hash required for user %q", u.Username)
	}
	enabled := 0
	if u.Enabled {
		enabled = 1
	}
	if u.ID > 0 {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO users (id, username, password_hash, enabled)
			VALUES (?, ?, ?, ?)
		`, u.ID, u.Username, u.PasswordHash, enabled)
		if err != nil {
			return 0, mapUserWriteErr(fmt.Sprintf("import user %d", u.ID), err)
		}
		return u.ID, nil
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO users (username, password_hash, enabled)
		VALUES (?, ?, ?)
	`, u.Username, u.PasswordHash, enabled)
	if err != nil {
		return 0, mapUserWriteErr("import user", err)
	}
	return res.LastInsertId()
}
