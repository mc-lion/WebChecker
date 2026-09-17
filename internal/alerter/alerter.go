package alerter

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"webchecker/internal/models"
	"webchecker/internal/store"
	"webchecker/internal/telegram"
)

type Alerter struct {
	store *store.Store
	tg    *telegram.Client
}

func New(st *store.Store, tg *telegram.Client) *Alerter {
	return &Alerter{store: st, tg: tg}
}

func (a *Alerter) Handle(ctx context.Context, mon models.Monitor, check models.Check) {
	state, err := a.store.GetOrCreateAlertState(ctx, mon.ID)
	if err != nil {
		slog.Error("alert state load failed", "monitor_id", mon.ID, "error", err)
		return
	}

	problem := !check.OK || check.Slow
	if !problem {
		if state.DownAlerted || state.SlowAlerted {
			msg := recoveryMessage(mon, check)
			if err := a.tg.Notify(ctx, msg); err != nil {
				slog.Error("telegram recovery notify failed", "monitor_id", mon.ID, "error", err)
			} else {
				state.DownAlerted = false
				state.SlowAlerted = false
			}
		} else {
			state.DownAlerted = false
			state.SlowAlerted = false
		}
		state.ConsecutiveProblems = 0
		if err := a.store.SaveAlertState(ctx, state); err != nil {
			slog.Error("alert state save failed", "monitor_id", mon.ID, "error", err)
		}
		return
	}

	state.ConsecutiveProblems++
	if state.ConsecutiveProblems >= mon.FailThreshold {
		if !check.OK && !state.DownAlerted {
			if err := a.tg.Notify(ctx, downMessage(mon, check)); err != nil {
				slog.Error("telegram down notify failed", "monitor_id", mon.ID, "error", err)
			} else {
				state.DownAlerted = true
			}
		}
		if check.Slow && !state.SlowAlerted {
			if err := a.tg.Notify(ctx, slowMessage(mon, check)); err != nil {
				slog.Error("telegram slow notify failed", "monitor_id", mon.ID, "error", err)
			} else {
				state.SlowAlerted = true
			}
		}
	}

	if err := a.store.SaveAlertState(ctx, state); err != nil {
		slog.Error("alert state save failed", "monitor_id", mon.ID, "error", err)
	}
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
