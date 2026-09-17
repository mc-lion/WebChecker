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

type runState struct {
	lastRun time.Time
	lastOK  bool
}

type Scheduler struct {
	store     *store.Store
	checker   *checker.Checker
	alerter   *alerter.Alerter
	workers   int
	retention time.Duration

	mu       sync.Mutex
	state    map[int64]runState
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
		state:     make(map[int64]runState),
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
		if _, ok := s.state[mon.ID]; !ok {
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
		seeded := runState{
			lastRun: initialLastRun(now, mon, last),
			lastOK:  last == nil || last.OK,
		}
		s.mu.Lock()
		if _, exists := s.state[mon.ID]; !exists {
			s.state[mon.ID] = seeded
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
		st, ok := s.state[mon.ID]
		wait := waitDuration(mon, !ok || st.lastOK)
		if ok && now.Sub(st.lastRun) < wait {
			continue
		}
		s.inFlight[mon.ID] = struct{}{}
		prevRun := st.lastRun
		st.lastRun = now
		s.state[mon.ID] = st
		select {
		case jobs <- mon:
		default:
			delete(s.inFlight, mon.ID)
			st.lastRun = prevRun
			s.state[mon.ID] = st
		}
	}
	for id := range s.state {
		if _, ok := active[id]; !ok {
			delete(s.state, id)
		}
	}
}

func initialLastRun(now time.Time, mon models.Monitor, last *models.Check) time.Time {
	if last == nil || last.CheckedAt.IsZero() {
		return hashPhase(now, mon.ID, monitorInterval(mon))
	}
	wait := waitDuration(mon, last.OK)
	if now.Sub(last.CheckedAt) < wait {
		return last.CheckedAt
	}
	return hashPhase(now, mon.ID, wait)
}

func waitDuration(mon models.Monitor, lastOK bool) time.Duration {
	if !lastOK {
		return retryInterval(mon)
	}
	return monitorInterval(mon)
}

func hashPhase(now time.Time, id int64, interval time.Duration) time.Time {
	sec := int64(interval / time.Second)
	if sec < 1 {
		sec = 1
	}
	offset := time.Duration(id%sec) * time.Second
	return now.Add(-interval + offset)
}

func retryInterval(mon models.Monitor) time.Duration {
	d := time.Duration(mon.RetryIntervalSeconds) * time.Second
	if d < time.Second {
		d = time.Second
	}
	limit := monitorInterval(mon)
	if d > limit {
		return limit
	}
	return d
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
	s.mu.Lock()
	if st, ok := s.state[mon.ID]; ok {
		st.lastOK = result.OK
		s.state[mon.ID] = st
	}
	s.mu.Unlock()
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
