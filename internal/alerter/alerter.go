package alerter

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"webchecker/internal/models"
	"webchecker/internal/store"
)

type Notifier interface {
	Notify(ctx context.Context, text string) error
	Enabled() bool
}

type Alerter struct {
	store      *store.Store
	tg         Notifier
	slowAlerts bool
}

func New(st *store.Store, tg Notifier, slowAlerts bool) *Alerter {
	return &Alerter{store: st, tg: tg, slowAlerts: slowAlerts}
}

func (a *Alerter) Handle(ctx context.Context, mon models.Monitor, check models.Check) {
	state, err := a.store.GetOrCreateAlertState(ctx, mon.ID)
	if err != nil {
		slog.Error("alert state load failed", "monitor_id", mon.ID, "error", err)
		return
	}

	next, sendDown, sendSlow, sendRecovery := evaluate(state, mon, check, a.slowAlerts)

	if sendRecovery {
		if err := a.notify(ctx, "recovery", mon, recoveryMessage(mon, check)); err != nil {
			slog.Error("telegram recovery notify failed", "monitor_id", mon.ID, "error", err)
		} else {
			next.DownAlerted = false
			next.SlowAlerted = false
		}
	}
	if sendDown {
		if err := a.notify(ctx, "down", mon, downMessage(mon, check)); err != nil {
			slog.Error("telegram down notify failed", "monitor_id", mon.ID, "error", err)
		} else {
			next.DownAlerted = true
		}
	}
	if sendSlow {
		if err := a.notify(ctx, "slow", mon, slowMessage(mon, check)); err != nil {
			slog.Error("telegram slow notify failed", "monitor_id", mon.ID, "error", err)
		} else {
			next.SlowAlerted = true
		}
	}

	if err := a.store.SaveAlertState(ctx, next); err != nil {
		slog.Error("alert state save failed", "monitor_id", mon.ID, "error", err)
	}
}

func (a *Alerter) notify(ctx context.Context, kind string, mon models.Monitor, text string) error {
	if a.tg == nil || !a.tg.Enabled() {
		slog.Warn("telegram alert skipped", "kind", kind, "monitor_id", mon.ID, "reason", "alerts disabled")
		return nil
	}
	slog.Info("telegram alert", "kind", kind, "monitor_id", mon.ID, "name", mon.Name)
	return a.tg.Notify(ctx, text)
}

func evaluate(state models.AlertState, mon models.Monitor, check models.Check, slowAlerts bool) (models.AlertState, bool, bool, bool) {
	slowProblem := check.Slow && slowAlerts
	problem := !check.OK || slowProblem
	next := state

	if !problem {
		sendRecovery := state.DownAlerted || state.SlowAlerted
		next.ConsecutiveProblems = 0
		if !sendRecovery {
			next.DownAlerted = false
			next.SlowAlerted = false
		}
		return next, false, false, sendRecovery
	}

	next.ConsecutiveProblems++
	if next.ConsecutiveProblems < mon.FailThreshold {
		return next, false, false, false
	}
	sendDown := !check.OK && !state.DownAlerted
	sendSlow := slowProblem && !state.SlowAlerted
	return next, sendDown, sendSlow, false
}

func downMessage(mon models.Monitor, check models.Check) string {
	got := "нет ответа"
	if check.StatusCode != nil {
		got = fmt.Sprintf("%d", *check.StatusCode)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "URL недоступен: %s\n%s\n", mon.Name, mon.URL)
	fmt.Fprintf(&b, "Ожидался статус %d, получен %s\n", mon.ExpectedStatus, got)
	fmt.Fprintf(&b, "Время ответа: %d мс\n", check.ResponseMS)
	if check.ErrorText != "" {
		fmt.Fprintf(&b, "%s", check.ErrorText)
	}
	return b.String()
}

func slowMessage(mon models.Monitor, check models.Check) string {
	got := "-"
	if check.StatusCode != nil {
		got = fmt.Sprintf("%d", *check.StatusCode)
	}
	return fmt.Sprintf(
		"Долгий ответ: %s\n%s\nСтатус: %s\nВремя ответа: %d мс (порог %d мс)",
		mon.Name, mon.URL, got, check.ResponseMS, mon.SlowThresholdMS,
	)
}

func recoveryMessage(mon models.Monitor, check models.Check) string {
	got := "-"
	if check.StatusCode != nil {
		got = fmt.Sprintf("%d", *check.StatusCode)
	}
	return fmt.Sprintf(
		"URL восстановлен: %s\n%s\nСтатус %s, время ответа %d мс",
		mon.Name, mon.URL, got, check.ResponseMS,
	)
}
