package scheduler

import (
	"testing"
	"time"

	"webchecker/internal/models"
)

func TestInitialLastRunUsesRecentCheck(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	checked := now.Add(-10 * time.Second)
	got := initialLastRun(now, models.Monitor{ID: 1, IntervalSeconds: 60}, &models.Check{CheckedAt: checked})
	if !got.Equal(checked) {
		t.Fatalf("got %s want %s", got, checked)
	}
	if now.Sub(got) >= 60*time.Second {
		t.Fatal("recent check should not be due yet")
	}
}

func TestInitialLastRunStaggersWithoutHistory(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	interval := 60 * time.Second

	due := initialLastRun(now, models.Monitor{ID: 60, IntervalSeconds: 60}, nil)
	if now.Sub(due) < interval {
		t.Fatalf("id 60 offset 0 should be due immediately, lastRun=%s", due)
	}

	delayed := initialLastRun(now, models.Monitor{ID: 30, IntervalSeconds: 60}, nil)
	wait := interval - now.Sub(delayed)
	if wait != 30*time.Second {
		t.Fatalf("id 30: want 30s wait, got %s (lastRun=%s)", wait, delayed)
	}
}

func TestInitialLastRunStaggersStaleHistory(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-2 * time.Hour)
	got := initialLastRun(now, models.Monitor{ID: 15, IntervalSeconds: 60}, &models.Check{CheckedAt: stale})
	want := now.Add(-60*time.Second + 15*time.Second)
	if !got.Equal(want) {
		t.Fatalf("stale history should use hash phase, got %s want %s", got, want)
	}
}

func TestInitialLastRunOffsetsSpread(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	seen := map[time.Duration]int{}
	for id := int64(1); id <= 60; id++ {
		last := initialLastRun(now, models.Monitor{ID: id, IntervalSeconds: 60}, nil)
		offset := last.Sub(now.Add(-60 * time.Second))
		seen[offset]++
	}
	if len(seen) != 60 {
		t.Fatalf("expected 60 distinct offsets, got %d", len(seen))
	}
}
