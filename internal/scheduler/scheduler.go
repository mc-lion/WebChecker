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
			s.mu.Lock()
			active := make(map[int64]struct{}, len(monitors))
			for _, mon := range monitors {
				active[mon.ID] = struct{}{}
				if _, busy := s.inFlight[mon.ID]; busy {
					continue
				}
				last, ok := s.lastRun[mon.ID]
				interval := time.Duration(mon.IntervalSeconds) * time.Second
				if interval < time.Second {
					interval = time.Second
				}
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
			s.mu.Unlock()
		}
	}
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
