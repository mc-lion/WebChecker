package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"webchecker/internal/alerter"
	"webchecker/internal/checker"
	"webchecker/internal/models"
	"webchecker/internal/store"
)

type Scheduler struct {
	store     *store.Store
	checker   *checker.Checker
	alerter   *alerter.Alerter
	workers   int
	retention time.Duration

	mu       sync.Mutex
	lastRun  map[int64]time.Time
	inFlight map[int64]struct{}
}

func New(st *store.Store, chk *checker.Checker, al *alerter.Alerter, workers int, retentionDays int) *Scheduler {
	if workers < 1 {
		workers = 1
	}
	return &Scheduler{
		store:     st,
		checker:   chk,
		alerter:   al,
		workers:   workers,
		retention: time.Duration(retentionDays) * 24 * time.Hour,
		lastRun:   make(map[int64]time.Time),
		inFlight:  make(map[int64]struct{}),
	}
}

func (s *Scheduler) Run(ctx context.Context) {
	jobs := make(chan models.Monitor, s.workers*2)
	var wg sync.WaitGroup
	for i := 0; i < s.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for mon := range jobs {
				s.runCheck(ctx, mon)
			}
		}()
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	retentionTicker := time.NewTicker(time.Hour)
	defer retentionTicker.Stop()

	s.cleanup(ctx)

	for {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		case <-retentionTicker.C:
			s.cleanup(ctx)
		case <-ticker.C:
			monitors, err := s.store.ListEnabledMonitors(ctx)
			if err != nil {
				if ctx.Err() != nil {
					close(jobs)
					wg.Wait()
					return
				}
				slog.Error("scheduler list monitors failed", "error", err)
				continue
			}

			now := time.Now()
			s.seedLastRuns(ctx, monitors, now)
			s.enqueueDue(monitors, now, jobs)
		}
	}
}

func (s *Scheduler) seedLastRuns(ctx context.Context, monitors []models.Monitor, now time.Time) {
	var missing []models.Monitor
	s.mu.Lock()
	for _, mon := range monitors {
		if _, ok := s.lastRun[mon.ID]; !ok {
			missing = append(missing, mon)
		}
	}
	s.mu.Unlock()

	for _, mon := range missing {
		last, err := s.store.LastCheck(ctx, mon.ID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("scheduler last check failed", "monitor_id", mon.ID, "error", err)
			last = nil
		}
		seeded := initialLastRun(now, mon, last)
		s.mu.Lock()
		if _, exists := s.lastRun[mon.ID]; !exists {
			s.lastRun[mon.ID] = seeded
		}
		s.mu.Unlock()
	}
}

func (s *Scheduler) enqueueDue(monitors []models.Monitor, now time.Time, jobs chan models.Monitor) {
	s.mu.Lock()
	defer s.mu.Unlock()

	active := make(map[int64]struct{}, len(monitors))
	for _, mon := range monitors {
		active[mon.ID] = struct{}{}
		if _, busy := s.inFlight[mon.ID]; busy {
			continue
		}
		last, ok := s.lastRun[mon.ID]
		interval := monitorInterval(mon)
		if ok && now.Sub(last) < interval {
			continue
		}
		s.inFlight[mon.ID] = struct{}{}
		s.lastRun[mon.ID] = now
		select {
		case jobs <- mon:
		default:
			delete(s.inFlight, mon.ID)
			delete(s.lastRun, mon.ID)
		}
	}
	for id := range s.lastRun {
		if _, ok := active[id]; !ok {
			delete(s.lastRun, id)
		}
	}
}

func initialLastRun(now time.Time, mon models.Monitor, last *models.Check) time.Time {
	interval := monitorInterval(mon)
	if last != nil && !last.CheckedAt.IsZero() && now.Sub(last.CheckedAt) < interval {
		return last.CheckedAt
	}
	sec := int64(interval / time.Second)
	if sec < 1 {
		sec = 1
	}
	offset := time.Duration(mon.ID%sec) * time.Second
	return now.Add(-interval + offset)
}

func monitorInterval(mon models.Monitor) time.Duration {
	interval := time.Duration(mon.IntervalSeconds) * time.Second
	if interval < time.Second {
		return time.Second
	}
	return interval
}

func (s *Scheduler) runCheck(ctx context.Context, mon models.Monitor) {
	defer func() {
		s.mu.Lock()
		delete(s.inFlight, mon.ID)
		s.mu.Unlock()
	}()

	result := s.checker.Check(ctx, mon)
	if !result.OK || result.Slow {
		status := 0
		if result.StatusCode != nil {
			status = *result.StatusCode
		}
		slog.Info("check problem", "monitor_id", mon.ID, "name", mon.Name, "ok", result.OK, "slow", result.Slow, "status", status, "ms", result.ResponseMS, "slow_threshold_ms", mon.SlowThresholdMS, "error", result.ErrorText)
	}
	if err := s.store.InsertCheck(ctx, result); err != nil {
		if ctx.Err() != nil {
			return
		}
		slog.Error("insert check failed", "monitor_id", mon.ID, "error", err)
		return
	}
	s.alerter.Handle(ctx, mon, result)
}

func (s *Scheduler) cleanup(ctx context.Context) {
	if s.retention <= 0 {
		return
	}
	cutoff := time.Now().UTC().Add(-s.retention)
	n, err := s.store.DeleteOldChecks(ctx, cutoff)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("retention cleanup failed", "error", err)
		}
		return
	}
	if n > 0 {
		slog.Info("deleted old checks", "count", n)
	}
}
