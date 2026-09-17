package telegram

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"webchecker/internal/models"
	"webchecker/internal/store"
)

type Command struct {
	Name string
	Arg  string
}

func ParseCommand(text string) (Command, bool) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return Command{}, false
	}
	name := strings.ToLower(fields[0])
	if at := strings.Index(name, "@"); at >= 0 {
		name = name[:at]
	}
	name = strings.TrimPrefix(name, "/")

	if name == "stat" || strings.HasPrefix(name, "stat") {
		arg := ""
		if name != "stat" {
			arg = strings.Trim(strings.TrimPrefix(name, "stat"), "_-:=#")
		}
		if arg == "" && len(fields) > 1 {
			arg = strings.Trim(fields[1], "_-:=#")
			arg = strings.TrimPrefix(strings.ToLower(arg), "id")
			arg = strings.Trim(arg, "_-:=#")
		}
		return Command{Name: "stat", Arg: arg}, true
	}

	switch name {
	case "list", "help", "start":
		cmd := Command{Name: name}
		if len(fields) > 1 {
			cmd.Arg = fields[1]
		}
		return cmd, true
	default:
		return Command{}, false
	}
}

func RunBot(ctx context.Context, client *Client, st *store.Store) {
	if client == nil || !client.Configured() {
		return
	}
	if err := client.DeleteWebhook(ctx); err != nil && ctx.Err() == nil {
		slog.Warn("telegram deleteWebhook failed", "error", err)
	}
	slog.Info("telegram bot commands started")

	var offset int64
	for {
		if ctx.Err() != nil {
			return
		}
		updates, err := client.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("telegram getUpdates failed", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(3 * time.Second):
			}
			continue
		}
		for _, upd := range updates {
			if upd.UpdateID >= offset {
				offset = upd.UpdateID + 1
			}
			text, chatID, ok := upd.textAndChat()
			if !ok {
				continue
			}
			if !sameChatID(chatID, client.ChatID()) {
				if _, isCmd := ParseCommand(text); isCmd {
					slog.Warn("telegram command ignored: chat id mismatch", "got", chatID, "want", client.ChatID())
				}
				continue
			}
			reply, err := handleCommand(ctx, st, text)
			if err != nil {
				slog.Error("telegram command failed", "error", err)
				reply = "Не удалось выполнить команду: " + err.Error()
			}
			if reply == "" {
				continue
			}
			if err := client.Send(ctx, reply); err != nil && ctx.Err() == nil {
				slog.Error("telegram command reply failed", "error", err)
			}
		}
	}
}

func handleCommand(ctx context.Context, st *store.Store, text string) (string, error) {
	cmd, ok := ParseCommand(text)
	if !ok {
		return "", nil
	}
	switch cmd.Name {
	case "help", "start":
		return commandHelp(), nil
	case "list":
		return commandList(ctx, st)
	case "stat":
		if cmd.Arg == "" {
			return commandStatAll(ctx, st)
		}
		id, err := strconv.ParseInt(cmd.Arg, 10, 64)
		if err != nil || id < 1 {
			return "Укажите числовой id: stat <id>", nil
		}
		return commandStatOne(ctx, st, id)
	default:
		return "", nil
	}
}

func commandHelp() string {
	return strings.Join([]string{
		"Команды webChecker:",
		"/list — список URL (id, название, url)",
		"/stat — id, название и статус всех URL",
		"/stat <id> — статистика по конкретному URL",
		"/help — эта справка",
	}, "\n")
}

func commandList(ctx context.Context, st *store.Store) (string, error) {
	monitors, err := st.ListMonitors(ctx)
	if err != nil {
		return "", err
	}
	if len(monitors) == 0 {
		return "Мониторов нет", nil
	}
	sort.Slice(monitors, func(i, j int) bool { return monitors[i].ID < monitors[j].ID })
	var b strings.Builder
	b.WriteString("Список URL:\n")
	for _, m := range monitors {
		fmt.Fprintf(&b, "%d | %s | %s\n", m.ID, m.Name, m.URL)
	}
	return strings.TrimSpace(b.String()), nil
}

func commandStatAll(ctx context.Context, st *store.Store) (string, error) {
	rows, err := st.Dashboard(ctx)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "Мониторов нет", nil
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	var b strings.Builder
	b.WriteString("Статусы URL:\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "%d | %s | %s\n", row.ID, row.Name, row.StateLabel())
	}
	return strings.TrimSpace(b.String()), nil
}

func commandStatOne(ctx context.Context, st *store.Store, id int64) (string, error) {
	mon, err := st.GetMonitor(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Sprintf("Монитор %d не найден. Команда: /stat <id>", id), nil
	}
	if err != nil {
		return "", err
	}
	stats, err := st.MonitorStats(ctx, id)
	if err != nil {
		return "", err
	}
	last, err := st.LastCheck(ctx, id)
	if err != nil {
		return "", err
	}

	row := models.DashboardRow{Monitor: mon}
	if last != nil {
		row.LastStatusCode = last.StatusCode
		ms := last.ResponseMS
		row.LastResponseMS = &ms
		ok := last.OK
		slow := last.Slow
		row.LastOK = &ok
		row.LastSlow = &slow
		t := last.CheckedAt
		row.LastCheckedAt = &t
	}

	lastStatus, lastMS, lastAt := "—", "—", "—"
	if row.LastStatusCode != nil {
		lastStatus = strconv.Itoa(*row.LastStatusCode)
	}
	if row.LastResponseMS != nil {
		lastMS = fmt.Sprintf("%d мс", *row.LastResponseMS)
	}
	if row.LastCheckedAt != nil {
		lastAt = row.LastCheckedAt.Local().Format("02.01.2006 15:04:05")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Статистика URL %d\n", mon.ID)
	fmt.Fprintf(&b, "Название: %s\n", mon.Name)
	fmt.Fprintf(&b, "URL: %s\n", mon.URL)
	fmt.Fprintf(&b, "Состояние: %s\n", row.StateLabel())
	fmt.Fprintf(&b, "Последний статус: %s\n", lastStatus)
	fmt.Fprintf(&b, "Время ответа: %s\n", lastMS)
	fmt.Fprintf(&b, "Последняя проверка: %s\n", lastAt)
	fmt.Fprintf(&b, "Uptime 24ч: %s (%d проверок)\n", formatPct(stats.Last24h), stats.Last24h.Total)
	fmt.Fprintf(&b, "Uptime 7д: %s (%d проверок)\n", formatPct(stats.Last7d), stats.Last7d.Total)
	fmt.Fprintf(&b, "Среднее / мин / макс 24ч: %s / %s / %s\n", formatAvg(stats.Last24h.AvgMS), formatMS(stats.Last24h.MinMS), formatMS(stats.Last24h.MaxMS))
	fmt.Fprintf(&b, "Интервал: %d с, при ошибке: %d с, ожидаемый статус: %d", mon.IntervalSeconds, mon.RetryIntervalSeconds, mon.ExpectedStatus)
	return b.String(), nil
}

func sameChatID(got int64, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	if strconv.FormatInt(got, 10) == want {
		return true
	}
	parsed, err := strconv.ParseInt(want, 10, 64)
	return err == nil && parsed == got
}

func formatPct(ps models.PeriodStats) string {
	if ps.Total == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", ps.UptimePct)
}

func formatMS(v *int) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%d мс", *v)
}

func formatAvg(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.0f мс", *v)
}
