package web

import (
	"net/url"
	"testing"
)

func TestMonitorFromFormValid(t *testing.T) {
	values := url.Values{
		"name":                   {"API"},
		"url":                    {"https://example.com/health"},
		"interval_seconds":       {"30"},
		"retry_interval_seconds": {"10"},
		"expected_status":        {"200"},
		"timeout_seconds":        {"5"},
		"slow_threshold_ms":      {"1500"},
		"fail_threshold":         {"3"},
		"slow_fail_threshold":    {"2"},
		"slow_alerts_enabled":    {"1"},
		"enabled":                {"1"},
	}
	m, err := monitorFromForm(values, defaultMonitor())
	if err != nil {
		t.Fatal(err)
	}
	if !m.Enabled || m.IntervalSeconds != 30 || m.RetryIntervalSeconds != 10 || m.SlowThresholdMS != 1500 {
		t.Fatalf("unexpected monitor: %+v", m)
	}
	if !m.SlowAlertsEnabled || m.SlowFailThreshold != 2 {
		t.Fatalf("slow alerts: enabled=%v threshold=%d", m.SlowAlertsEnabled, m.SlowFailThreshold)
	}
}

func TestMonitorFromFormUserAgentID(t *testing.T) {
	values := url.Values{
		"name":                   {"API"},
		"url":                    {"https://example.com/health"},
		"interval_seconds":       {"30"},
		"retry_interval_seconds": {"10"},
		"expected_status":        {"200"},
		"timeout_seconds":        {"5"},
		"slow_threshold_ms":      {"1500"},
		"fail_threshold":         {"3"},
		"slow_fail_threshold":    {"3"},
		"slow_alerts_enabled":    {"1"},
		"user_agent_id":          {"4"},
	}
	m, err := monitorFromForm(values, defaultMonitor())
	if err != nil {
		t.Fatal(err)
	}
	if m.UserAgentID != 4 {
		t.Fatalf("UserAgentID = %d, want 4", m.UserAgentID)
	}
}

func TestMonitorFromFormRejectsBadUserAgentID(t *testing.T) {
	values := url.Values{
		"name":                   {"API"},
		"url":                    {"https://example.com/health"},
		"interval_seconds":       {"30"},
		"retry_interval_seconds": {"10"},
		"expected_status":        {"200"},
		"timeout_seconds":        {"5"},
		"slow_threshold_ms":      {"1500"},
		"fail_threshold":         {"3"},
		"slow_fail_threshold":    {"3"},
		"user_agent_id":          {"nope"},
	}
	if _, err := monitorFromForm(values, defaultMonitor()); err == nil {
		t.Fatal("expected user agent error")
	}
}

func TestMonitorFromFormRejectsFTP(t *testing.T) {
	values := url.Values{
		"name":                   {"Bad"},
		"url":                    {"ftp://example.com/file"},
		"interval_seconds":       {"30"},
		"retry_interval_seconds": {"10"},
		"expected_status":        {"200"},
		"timeout_seconds":        {"5"},
		"slow_threshold_ms":      {"1500"},
		"fail_threshold":         {"3"},
		"slow_fail_threshold":    {"3"},
	}
	if _, err := monitorFromForm(values, defaultMonitor()); err == nil {
		t.Fatal("expected error")
	}
}

func TestMonitorFromFormMinInterval(t *testing.T) {
	values := url.Values{
		"name":                {"API"},
		"url":                 {"https://example.com/health"},
		"interval_seconds":    {"5"},
		"expected_status":     {"200"},
		"timeout_seconds":     {"5"},
		"slow_threshold_ms":   {"1500"},
		"fail_threshold":      {"3"},
		"slow_fail_threshold": {"3"},
	}
	if _, err := monitorFromForm(values, defaultMonitor()); err == nil {
		t.Fatal("expected interval error")
	}
}

func TestMonitorFromFormRetryTooLarge(t *testing.T) {
	values := url.Values{
		"name":                   {"API"},
		"url":                    {"https://example.com/health"},
		"interval_seconds":       {"30"},
		"retry_interval_seconds": {"60"},
		"expected_status":        {"200"},
		"timeout_seconds":        {"5"},
		"slow_threshold_ms":      {"1500"},
		"fail_threshold":         {"3"},
		"slow_fail_threshold":    {"3"},
	}
	if _, err := monitorFromForm(values, defaultMonitor()); err == nil {
		t.Fatal("expected retry interval error")
	}
}

func TestMonitorFromFormSlowAlertsOff(t *testing.T) {
	values := url.Values{
		"name":                   {"API"},
		"url":                    {"https://example.com/health"},
		"interval_seconds":       {"30"},
		"retry_interval_seconds": {"10"},
		"expected_status":        {"200"},
		"timeout_seconds":        {"5"},
		"slow_threshold_ms":      {"1500"},
		"fail_threshold":         {"3"},
		"slow_fail_threshold":    {"5"},
		"enabled":                {"1"},
	}
	m, err := monitorFromForm(values, defaultMonitor())
	if err != nil {
		t.Fatal(err)
	}
	if m.SlowAlertsEnabled {
		t.Fatal("slow_alerts_enabled unchecked must be false")
	}
	if m.SlowFailThreshold != 5 {
		t.Fatalf("SlowFailThreshold=%d, want 5", m.SlowFailThreshold)
	}
}
