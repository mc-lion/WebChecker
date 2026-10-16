package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr          string
	AuthUser          string
	AuthPassword      string
	AuthEnvAlways     bool
	JWTSecret         string
	JWTTTL            time.Duration
	MySQLHost         string
	MySQLPort         string
	MySQLUser         string
	MySQLPassword     string
	MySQLDatabase     string
	TelegramEnabled   bool
	TelegramBotToken  string
	TelegramChatID    string
	TelegramSlowAlerts bool
	StatsRetentionDays int
	CheckerWorkers     int
	BlockPrivateHosts  bool
	// EnableHSTS включает Strict-Transport-Security. По умолчанию выключено:
	// за reverse-proxy с HTTPS включите, за прямым HTTP — оставьте выключенным.
	EnableHSTS bool
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:           envOr("HTTP_ADDR", ":8080"),
		AuthUser:           strings.TrimSpace(os.Getenv("AUTH_USER")),
		AuthPassword:       os.Getenv("AUTH_PASSWORD"),
		JWTSecret:          os.Getenv("JWT_SECRET"),
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
	cfg.AuthEnvAlways, err = envBool("AUTH_ENV_ALWAYS", false)
	if err != nil {
		return Config{}, err
	}
	cfg.JWTTTL, err = envDuration("JWT_TTL", 24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	cfg.TelegramEnabled, err = envBool("TELEGRAM_ENABLED", cfg.TelegramBotToken != "" && cfg.TelegramChatID != "")
	if err != nil {
		return Config{}, err
	}
	cfg.TelegramSlowAlerts, err = envBool("TELEGRAM_SLOW_ALERTS", true)
	if err != nil {
		return Config{}, err
	}
	// По умолчанию включено: без блокировки авторизованный пользователь может
	// направить проверку на cloud metadata (169.254.169.254), localhost или
	// соседние сервисы в приватной сети. Если нужно мониторить внутренний
	// http://app:8080 — ставьте CHECK_BLOCK_PRIVATE_HOSTS=false явно.
	cfg.BlockPrivateHosts, err = envBool("CHECK_BLOCK_PRIVATE_HOSTS", true)
	if err != nil {
		return Config{}, err
	}
	cfg.EnableHSTS, err = envBool("HTTP_HSTS", false)
	if err != nil {
		return Config{}, err
	}

	if cfg.AuthUser == "" || cfg.AuthPassword == "" {
		if os.Getenv("BASIC_AUTH_USER") != "" || os.Getenv("BASIC_AUTH_PASSWORD") != "" {
			return Config{}, fmt.Errorf("AUTH_USER and AUTH_PASSWORD are required (BASIC_AUTH_* больше не поддерживаются, переименуйте в AUTH_*)")
		}
		return Config{}, fmt.Errorf("AUTH_USER and AUTH_PASSWORD are required")
	}
	if len(cfg.AuthPassword) < 8 {
		return Config{}, fmt.Errorf("AUTH_PASSWORD too short: minimum 8 characters")
	}
	if len(cfg.JWTSecret) < 32 {
		return Config{}, fmt.Errorf("JWT_SECRET is required and must be at least 32 characters")
	}
	if cfg.JWTTTL < time.Minute {
		return Config{}, fmt.Errorf("JWT_TTL must be at least 1m")
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

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid duration for %s: %q", key, raw)
	}
	return d, nil
}
