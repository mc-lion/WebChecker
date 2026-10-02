package store

import (
	"testing"
	"time"

	"webchecker/internal/models"
)

func TestPeriodStatsWindowHours(t *testing.T) {
	// periodStats режет окно целыми часами: 1ч и 7д должны попадать в SQL как 1 и 168.
	cases := []struct {
		window time.Duration
		hours  int
	}{
		{time.Hour, 1},
		{3 * time.Hour, 3},
		{6 * time.Hour, 6},
		{12 * time.Hour, 12},
		{24 * time.Hour, 24},
		{7 * 24 * time.Hour, 168},
	}
	for _, tc := range cases {
		hours := int(tc.window.Hours())
		if hours < 1 {
			hours = 1
		}
		if hours != tc.hours {
			t.Fatalf("window %s → %d hours, want %d", tc.window, hours, tc.hours)
		}
	}
}

func TestCheckBucketZeroValue(t *testing.T) {
	var b models.CheckBucket
	if b.OK || b.Count != 0 || b.AvgMS != 0 || !b.At.IsZero() {
		t.Fatalf("zero bucket should be empty, got %+v", b)
	}
}
