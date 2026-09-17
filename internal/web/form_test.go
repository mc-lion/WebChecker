package web

import (
	"net/url"
	"testing"
)

func TestMonitorFromFormValid(t *testing.T) {
	values := url.Values{
		"name":              {"API"},
		"url":               {"https://example.com/health"},
		"interval_seconds":  {"30"},
		"expected_status":   {"200"},
		"timeout_seconds":   {"5"},
		"slow_threshold_ms": {"1500"},
		"fail_threshold":    {"3"},
		"enabled":           {"1"},
	}
	m, err := monitorFromForm(values, defaultMonitor())
	if err != nil {
		t.Fatal(err)
	}
	if !m.Enabled || m.IntervalSeconds != 30 || m.SlowThresholdMS != 1500 {
		t.Fatalf("unexpected monitor: %+v", m)
	}
}

func TestMonitorFromFormRejectsFTP(t *testing.T) {
	values := url.Values{
		"name":              {"Bad"},
		"url":               {"ftp://example.com/file"},
		"interval_seconds":  {"30"},
		"expected_status":   {"200"},
		"timeout_seconds":   {"5"},
		"slow_threshold_ms": {"1500"},
		"fail_threshold":    {"3"},
	}
	if _, err := monitorFromForm(values, defaultMonitor()); err == nil {
		t.Fatal("expected error")
	}
}

func TestMonitorFromFormMinInterval(t *testing.T) {
	values := url.Values{
		"name":              {"API"},
		"url":               {"https://example.com/health"},
		"interval_seconds":  {"5"},
		"expected_status":   {"200"},
		"timeout_seconds":   {"5"},
		"slow_threshold_ms": {"1500"},
		"fail_threshold":    {"3"},
	}
	if _, err := monitorFromForm(values, defaultMonitor()); err == nil {
		t.Fatal("expected interval error")
	}
}
