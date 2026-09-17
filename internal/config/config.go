package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPAddr           string
	BasicAuthUser      string
	BasicAuthPassword  string
	MySQLHost          string
	MySQLPort          string
	MySQLUser          string
	MySQLPassword      string
	MySQLDatabase      string
	TelegramEnabled    bool
	TelegramBotToken   string
	TelegramChatID     string
	TelegramSlowAlerts bool
	StatsRetentionDays int
	CheckerWorkers     int
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:           envOr("HTTP_ADDR", ":8080"),
		BasicAuthUser:      strings.TrimSpace(os.Getenv("BASIC_AUTH_USER")),
		BasicAuthPassword:  os.Getenv("BASIC_AUTH_PASSWORD"),
		MySQLHost:          envOr("MYSQL_HOST", "mysql"),
		MySQLPort:          envOr("MYSQL_PORT", "3306"),
		MySQLUser:          strings.TrimSpace(os.Getenv("MYSQL_USER")),
		MySQLPassword:      os.Getenv("MYSQL_PASSWORD"),
		MySQLDatabase:      strings.TrimSpace(os.Getenv("MYSQL_DATABASE")),
		TelegramBotToken:   strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		TelegramChatID:     strings.TrimSpace(os.Getenv("TELEGRAM_CHAT_ID")),
		StatsRetentionDays: envInt("STATS_RETENTION_DAYS", 30),
		CheckerWorkers:     envInt("CHECKER_WORKERS", 8),
	}

	var err error
	cfg.TelegramEnabled, err = envBool("TELEGRAM_ENABLED", cfg.TelegramBotToken != "" && cfg.TelegramChatID != "")
	if err != nil {
		return Config{}, err
	}
	cfg.TelegramSlowAlerts, err = envBool("TELEGRAM_SLOW_ALERTS", true)
	if err != nil {
		return Config{}, err
	}

	if cfg.BasicAuthUser == "" || cfg.BasicAuthPassword == "" {
		return Config{}, fmt.Errorf("BASIC_AUTH_USER and BASIC_AUTH_PASSWORD are required")
	}
	if cfg.MySQLUser == "" || cfg.MySQLDatabase == "" {
		return Config{}, fmt.Errorf("MYSQL_USER and MYSQL_DATABASE are required")
	}
	if cfg.StatsRetentionDays < 1 {
		return Config{}, fmt.Errorf("STATS_RETENTION_DAYS must be >= 1")
	}
	if cfg.CheckerWorkers < 1 {
		return Config{}, fmt.Errorf("CHECKER_WORKERS must be >= 1")
	}
	if cfg.TelegramEnabled && (cfg.TelegramBotToken == "" || cfg.TelegramChatID == "") {
		return Config{}, fmt.Errorf("TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID are required when TELEGRAM_ENABLED=true")
	}

	return cfg, nil
}

func (c Config) MySQLDSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&loc=UTC&charset=utf8mb4&timeout=5s&readTimeout=30s&writeTimeout=30s",
		c.MySQLUser, c.MySQLPassword, c.MySQLHost, c.MySQLPort, c.MySQLDatabase)
}

func (c Config) TelegramConfigured() bool {
	return c.TelegramBotToken != "" && c.TelegramChatID != ""
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func envBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value for %s: %q", key, raw)
	}
}
