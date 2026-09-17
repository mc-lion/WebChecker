package web

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"webchecker/internal/models"
)

const (
	minIntervalSeconds = 10
	maxIntervalSeconds = 86400
	minTimeoutSeconds  = 1
	maxTimeoutSeconds  = 120
	minSlowMS          = 1
	maxSlowMS          = 600000
	minFailThreshold   = 1
	maxFailThreshold   = 100
	maxNameLen         = 255
	maxURLLen          = 2048
)

func defaultMonitor() models.Monitor {
	return models.Monitor{
		IntervalSeconds: 60,
		ExpectedStatus:  200,
		TimeoutSeconds:  10,
		SlowThresholdMS: 3000,
		FailThreshold:   3,
		Enabled:         true,
	}
}

func monitorFromForm(values url.Values, existing models.Monitor) (models.Monitor, error) {
	m := existing
	m.Name = strings.TrimSpace(values.Get("name"))
	m.URL = strings.TrimSpace(values.Get("url"))
	m.Enabled = values.Get("enabled") == "1"

	var err error
	m.IntervalSeconds, err = parseIntField(values.Get("interval_seconds"), "интервал")
	if err != nil {
		return m, err
	}
	m.ExpectedStatus, err = parseIntField(values.Get("expected_status"), "ожидаемый статус")
	if err != nil {
		return m, err
	}
	m.TimeoutSeconds, err = parseIntField(values.Get("timeout_seconds"), "таймаут")
	if err != nil {
		return m, err
	}
	m.SlowThresholdMS, err = parseIntField(values.Get("slow_threshold_ms"), "порог времени ответа")
	if err != nil {
		return m, err
	}
	m.FailThreshold, err = parseIntField(values.Get("fail_threshold"), "порог подряд ошибок")
	if err != nil {
		return m, err
	}

	if m.Name == "" {
		return m, fmt.Errorf("укажите имя")
	}
	if len(m.Name) > maxNameLen {
		return m, fmt.Errorf("имя слишком длинное")
	}
	if m.URL == "" {
		return m, fmt.Errorf("укажите URL")
	}
	if len(m.URL) > maxURLLen {
		return m, fmt.Errorf("URL слишком длинный")
	}
	parsed, err := url.ParseRequestURI(m.URL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return m, fmt.Errorf("URL должен начинаться с http:// или https://")
	}
	if m.IntervalSeconds < minIntervalSeconds || m.IntervalSeconds > maxIntervalSeconds {
		return m, fmt.Errorf("интервал должен быть от %d до %d секунд", minIntervalSeconds, maxIntervalSeconds)
	}
	if m.ExpectedStatus < 100 || m.ExpectedStatus > 599 {
		return m, fmt.Errorf("ожидаемый статус должен быть от 100 до 599")
	}
	if m.TimeoutSeconds < minTimeoutSeconds || m.TimeoutSeconds > maxTimeoutSeconds {
		return m, fmt.Errorf("таймаут должен быть от %d до %d секунд", minTimeoutSeconds, maxTimeoutSeconds)
	}
	if m.SlowThresholdMS < minSlowMS || m.SlowThresholdMS > maxSlowMS {
		return m, fmt.Errorf("порог времени ответа должен быть от %d до %d мс", minSlowMS, maxSlowMS)
	}
	if m.FailThreshold < minFailThreshold || m.FailThreshold > maxFailThreshold {
		return m, fmt.Errorf("порог подряд ошибок должен быть от %d до %d", minFailThreshold, maxFailThreshold)
	}
	return m, nil
}

func parseIntField(raw, label string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("укажите %s", label)
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("некорректное значение: %s", label)
	}
	return n, nil
}
