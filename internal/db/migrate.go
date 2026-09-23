package db

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"webchecker/migrations"
)

func Migrate(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(255) NOT NULL PRIMARY KEY,
			applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		files = append(files, entry.Name())
	}
	sort.Strings(files)

	for _, name := range files {
		var exists int
		err := database.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", name).Scan(&exists)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if exists > 0 {
			continue
		}

		raw, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		slog.Info("applying migration", "version", name)
		if err := applyMigration(ctx, database, name, string(raw)); err != nil {
			return err
		}
	}

	return nil
}

// applyMigration выполняет все statement'ы файла на одном соединении. DDL в
// MySQL не откатывается, поэтому миграции написаны идемпотентно (IF NOT EXISTS
// и проверки information_schema) и переживают повторный запуск после сбоя. Для
// этого нужна одна сессия: сессионные переменные и PREPARE не видны на другом
// соединении пула.
func applyMigration(ctx context.Context, database *sql.DB, name, script string) error {
	conn, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migration %s: acquire connection: %w", name, err)
	}
	defer conn.Close()

	for _, stmt := range splitSQL(script) {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", name); err != nil {
		return fmt.Errorf("record migration %s: %w", name, err)
	}
	return nil
}

func splitSQL(script string) []string {
	var stmts []string
	var b strings.Builder
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
		if strings.HasSuffix(trimmed, ";") {
			stmt := strings.TrimSpace(b.String())
			stmt = strings.TrimSuffix(stmt, ";")
			stmt = strings.TrimSpace(stmt)
			if stmt != "" {
				stmts = append(stmts, stmt)
			}
			b.Reset()
		}
	}
	if leftover := strings.TrimSpace(b.String()); leftover != "" {
		stmts = append(stmts, leftover)
	}
	return stmts
}
