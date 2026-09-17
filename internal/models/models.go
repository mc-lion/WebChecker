package models

import "time"

type Monitor struct {
	ID                   int64     `json:"id"`
	Name                 string    `json:"name"`
	URL                  string    `json:"url"`
	IntervalSeconds      int       `json:"interval_seconds"`
	RetryIntervalSeconds int       `json:"retry_interval_seconds"`
	ExpectedStatus       int       `json:"expected_status"`
	TimeoutSeconds       int       `json:"timeout_seconds"`
	SlowThresholdMS      int       `json:"slow_threshold_ms"`
	FailThreshold        int       `json:"fail_threshold"`
	Enabled              bool      `json:"enabled"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type Check struct {
	ID         int64     `json:"id"`
	MonitorID  int64     `json:"monitor_id"`
	CheckedAt  time.Time `json:"checked_at"`
	StatusCode *int      `json:"status_code,omitempty"`
	ResponseMS int       `json:"response_ms"`
	OK         bool      `json:"ok"`
	Slow       bool      `json:"slow"`
	ErrorText  string    `json:"error_text,omitempty"`
}

type AlertState struct {
	MonitorID           int64     `json:"monitor_id"`
	ConsecutiveProblems int       `json:"consecutive_problems"`
	DownAlerted         bool      `json:"down_alerted"`
	SlowAlerted         bool      `json:"slow_alerted"`
	UpdatedAt           time.Time `json:"updated_at,omitempty"`
}

type Dump struct {
	Version     int          `json:"version"`
	ExportedAt  time.Time    `json:"exported_at"`
	Monitors    []Monitor    `json:"monitors"`
	Checks      []Check      `json:"checks"`
	AlertStates []AlertState `json:"alert_states"`
}

const DumpVersion = 1

type DashboardRow struct {
	Monitor
	LastStatusCode *int
	LastResponseMS *int
	LastOK         *bool
	LastSlow       *bool
	LastError      string
	LastCheckedAt  *time.Time
	Uptime24h      *float64
}

type PeriodStats struct {
	Total     int
	OKCount   int
	SlowCount int
	UptimePct float64
	AvgMS     *float64
	MinMS     *int
	MaxMS     *int
}

type MonitorStats struct {
	Last24h PeriodStats
	Last7d  PeriodStats
}

func (r DashboardRow) State() string {
	if !r.Enabled {
		return "disabled"
	}
	if r.LastCheckedAt == nil {
		return "pending"
	}
	if r.LastOK != nil && !*r.LastOK {
		return "down"
	}
	if r.LastSlow != nil && *r.LastSlow {
		return "slow"
	}
	return "ok"
}

func StatusLabel(state string) string {
	switch state {
	case "ok":
		return "OK"
	case "down":
		return "Недоступен"
	case "slow":
		return "Медленно"
	case "disabled":
		return "Выключен"
	case "pending":
		return "Ожидание"
	default:
		return state
	}
}

func (r DashboardRow) StateLabel() string {
	return StatusLabel(r.State())
}
