package models

import "time"

type Monitor struct {
	ID              int64
	Name            string
	URL             string
	IntervalSeconds int
	ExpectedStatus  int
	TimeoutSeconds  int
	SlowThresholdMS int
	FailThreshold   int
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Check struct {
	ID         int64
	MonitorID  int64
	CheckedAt  time.Time
	StatusCode *int
	ResponseMS int
	OK         bool
	Slow       bool
	ErrorText  string
}

type AlertState struct {
	MonitorID           int64
	ConsecutiveProblems int
	DownAlerted         bool
	SlowAlerted         bool
	UpdatedAt           time.Time
}

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
