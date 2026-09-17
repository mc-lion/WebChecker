package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"webchecker/internal/config"
)

func Connect(ctx context.Context, cfg config.Config) (*sql.DB, error) {
	dsn := cfg.MySQLDSN()
	var lastErr error

	for i := 1; i <= 30; i++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		database, err := sql.Open("mysql", dsn)
		if err != nil {
			return nil, fmt.Errorf("open mysql: %w", err)
		}

		database.SetMaxOpenConns(25)
		database.SetMaxIdleConns(10)
		database.SetConnMaxLifetime(5 * time.Minute)

		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = database.PingContext(pingCtx)
		cancel()
		if err == nil {
			slog.Info("connected to mysql", "host", cfg.MySQLHost, "database", cfg.MySQLDatabase)
			return database, nil
		}

		lastErr = err
		_ = database.Close()
		slog.Warn("mysql not ready, retrying", "attempt", i, "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	return nil, fmt.Errorf("mysql ping failed after retries: %w", lastErr)
}
