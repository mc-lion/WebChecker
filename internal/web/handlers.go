package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"webchecker/internal/config"
	"webchecker/internal/models"
	"webchecker/internal/store"
	"webchecker/internal/telegram"
	webassets "webchecker/web"
)

type Server struct {
	cfg   config.Config
	store *store.Store
	tg    *telegram.Client
	pages map[string]*template.Template
}

func New(cfg config.Config, st *store.Store, tg *telegram.Client) (*Server, error) {
	pages, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, store: st, tg: tg, pages: pages}, nil
}

func (s *Server) Handler() http.Handler {
	protected := http.NewServeMux()
	protected.Handle("GET /static/", staticHandler())
	protected.HandleFunc("GET /{$}", s.dashboard)
	protected.HandleFunc("GET /monitors/new", s.newForm)
	protected.HandleFunc("POST /monitors", s.create)
	protected.HandleFunc("GET /monitors/{id}", s.show)
	protected.HandleFunc("GET /monitors/{id}/edit", s.editForm)
	protected.HandleFunc("GET /monitors/{id}/checks.json", s.checksJSON)
	protected.HandleFunc("POST /monitors/{id}", s.update)
	protected.HandleFunc("POST /monitors/{id}/delete", s.delete)
	protected.HandleFunc("POST /monitors/{id}/toggle", s.toggle)
	protected.HandleFunc("GET /settings", s.settings)
	protected.HandleFunc("POST /settings/telegram-test", s.telegramTest)
	protected.HandleFunc("GET /settings/export", s.exportDump)
	protected.HandleFunc("POST /settings/import", s.importDump)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("/", basicAuth(s.cfg.BasicAuthUser, s.cfg.BasicAuthPassword, protected))
	return logging(mux)
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	rows, err := s.store.Dashboard(r.Context())
	if err != nil {
		s.serverError(w, "dashboard", err)
		return
	}
	s.render(w, r, "dashboard.html", map[string]any{
		"Title":   "Дашборд",
		"Active":  "dashboard",
		"Rows":    rows,
		"Refresh": true,
	})
}

func (s *Server) newForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "form.html", map[string]any{
		"Title":   "Новый URL",
		"Active":  "dashboard",
		"Monitor": defaultMonitor(),
		"Action":  "/monitors",
		"IsNew":   true,
	})
}

func (s *Server) editForm(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	mon, err := s.store.GetMonitor(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, "get monitor", err)
		return
	}
	s.render(w, r, "form.html", map[string]any{
		"Title":   "Редактирование",
		"Active":  "dashboard",
		"Monitor": mon,
		"Action":  fmt.Sprintf("/monitors/%d", mon.ID),
		"IsNew":   false,
	})
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректная форма", http.StatusBadRequest)
		return
	}
	mon, err := monitorFromForm(r.PostForm, defaultMonitor())
	if err != nil {
		s.render(w, r, "form.html", map[string]any{
			"Title":   "Новый URL",
			"Active":  "dashboard",
			"Monitor": mon,
			"Action":  "/monitors",
			"IsNew":   true,
			"Error":   err.Error(),
		})
		return
	}
	if _, err := s.store.CreateMonitor(r.Context(), mon); err != nil {
		s.serverError(w, "create monitor", err)
		return
	}
	http.Redirect(w, r, "/?msg=created", http.StatusSeeOther)
}

func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	existing, err := s.store.GetMonitor(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, "get monitor", err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректная форма", http.StatusBadRequest)
		return
	}
	mon, err := monitorFromForm(r.PostForm, existing)
	mon.ID = id
	if err != nil {
		s.render(w, r, "form.html", map[string]any{
			"Title":   "Редактирование",
			"Active":  "dashboard",
			"Monitor": mon,
			"Action":  fmt.Sprintf("/monitors/%d", id),
			"IsNew":   false,
			"Error":   err.Error(),
		})
		return
	}
	if err := s.store.UpdateMonitor(r.Context(), mon); err != nil {
		s.serverError(w, "update monitor", err)
		return
	}
	http.Redirect(w, r, "/?msg=updated", http.StatusSeeOther)
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.store.DeleteMonitor(r.Context(), id); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.serverError(w, "delete monitor", err)
		return
	}
	http.Redirect(w, r, "/?msg=deleted", http.StatusSeeOther)
}

func (s *Server) toggle(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	mon, err := s.store.GetMonitor(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, "get monitor", err)
		return
	}
	if err := s.store.SetMonitorEnabled(r.Context(), id, !mon.Enabled); err != nil {
		s.serverError(w, "toggle monitor", err)
		return
	}
	http.Redirect(w, r, "/?msg=toggled", http.StatusSeeOther)
}

func (s *Server) show(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	mon, err := s.store.GetMonitor(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, "get monitor", err)
		return
	}
	stats, err := s.store.MonitorStats(r.Context(), id)
	if err != nil {
		s.serverError(w, "monitor stats", err)
		return
	}
	checks, err := s.store.ListRecentChecks(r.Context(), id, 100)
	if err != nil {
		s.serverError(w, "recent checks", err)
		return
	}
	s.render(w, r, "stats.html", map[string]any{
		"Title":   mon.Name,
		"Active":  "dashboard",
		"Monitor": mon,
		"Stats":   stats,
		"Checks":  checks,
	})
}

func (s *Server) checksJSON(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.store.GetMonitor(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		s.serverError(w, "get monitor", err)
		return
	}
	checks, err := s.store.ListRecentChecks(r.Context(), id, 200)
	if err != nil {
		s.serverError(w, "checks json", err)
		return
	}

	type point struct {
		T  string `json:"t"`
		MS int    `json:"ms"`
		OK bool   `json:"ok"`
	}
	points := make([]point, 0, len(checks))
	for i := len(checks) - 1; i >= 0; i-- {
		c := checks[i]
		points = append(points, point{
			T:  c.CheckedAt.Local().Format("15:04:05"),
			MS: c.ResponseMS,
			OK: c.OK,
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(points)
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "settings.html", map[string]any{
		"Title":              "Настройки",
		"Active":             "settings",
		"TelegramEnabled":    s.cfg.TelegramEnabled,
		"TelegramConfigured": s.cfg.TelegramConfigured(),
		"TelegramSlowAlerts": s.cfg.TelegramSlowAlerts,
		"RetentionDays":      s.cfg.StatsRetentionDays,
		"CheckerWorkers":     s.cfg.CheckerWorkers,
	})
}

func (s *Server) telegramTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.tg.Send(ctx, "webChecker: тестовое сообщение. Telegram подключён."); err != nil {
		http.Redirect(w, r, "/settings?err="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?msg=telegram_ok", http.StatusSeeOther)
}

const maxImportBytes = 32 << 20

func (s *Server) exportDump(w http.ResponseWriter, r *http.Request) {
	dump, err := s.store.ExportDump(r.Context())
	if err != nil {
		s.serverError(w, "export dump", err)
		return
	}
	filename := fmt.Sprintf("webchecker-%s.json", time.Now().Local().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(dump); err != nil {
		slog.Error("encode dump", "error", err)
	}
}

func (s *Server) importDump(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+1024)
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		http.Redirect(w, r, "/settings?err="+urlQuery("Не удалось прочитать файл. Максимум 32 МБ."), http.StatusSeeOther)
		return
	}
	file, header, err := r.FormFile("dump")
	if err != nil {
		http.Redirect(w, r, "/settings?err="+urlQuery("Выберите JSON-файл для импорта"), http.StatusSeeOther)
		return
	}
	defer file.Close()
	if header.Size > maxImportBytes {
		http.Redirect(w, r, "/settings?err="+urlQuery("Файл слишком большой (максимум 32 МБ)"), http.StatusSeeOther)
		return
	}

	var dump models.Dump
	dec := json.NewDecoder(file)
	if err := dec.Decode(&dump); err != nil {
		http.Redirect(w, r, "/settings?err="+urlQuery("Файл не является корректным JSON-дампом"), http.StatusSeeOther)
		return
	}
	if err := s.store.ImportDump(r.Context(), dump); err != nil {
		http.Redirect(w, r, "/settings?err="+urlQuery(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?msg=imported", http.StatusSeeOther)
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		http.Error(w, "db unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	tmpl, ok := s.pages[page]
	if !ok {
		s.serverError(w, "missing template "+page, fmt.Errorf("template not found"))
		return
	}
	if _, exists := data["Flash"]; !exists {
		data["Flash"] = flashMessage(r.URL.Query().Get("msg"))
	}
	if _, exists := data["Error"]; !exists {
		data["Error"] = r.URL.Query().Get("err")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		slog.Error("render template failed", "page", page, "error", err)
	}
}

func (s *Server) serverError(w http.ResponseWriter, op string, err error) {
	slog.Error(op, "error", err)
	http.Error(w, "Внутренняя ошибка сервера", http.StatusInternalServerError)
}

func parseTemplates() (map[string]*template.Template, error) {
	funcs := template.FuncMap{
		"stateLabel": stateLabel,
		"fmtTime":    fmtTime,
		"fmtTimeVal": fmtTimeVal,
		"fmtPct":     fmtPct,
		"fmtMS":      fmtMS,
		"fmtFloat":   fmtFloat,
		"statusText": statusText,
	}
	pages := []string{"dashboard.html", "form.html", "stats.html", "settings.html"}
	out := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		t, err := template.New("").Funcs(funcs).ParseFS(webassets.FS, "templates/layout.html", "templates/"+page)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", page, err)
		}
		out[page] = t
	}
	return out, nil
}

func staticHandler() http.Handler {
	sub, err := fs.Sub(webassets.FS, "static")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}

func parseID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func flashMessage(code string) string {
	switch code {
	case "created":
		return "URL добавлен"
	case "updated":
		return "Изменения сохранены"
	case "deleted":
		return "URL удалён"
	case "toggled":
		return "Состояние монитора изменено"
	case "telegram_ok":
		return "Тестовое сообщение отправлено"
	case "imported":
		return "Данные импортированы"
	default:
		return ""
	}
}

func stateLabel(state string) string {
	return models.StatusLabel(state)
}

func fmtTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "—"
	}
	return fmtTimeVal(*t)
}

func fmtTimeVal(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("02.01.2006 15:04:05")
}

func fmtPct(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", *v)
}

func fmtMS(v *int) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%d мс", *v)
}

func fmtFloat(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.0f мс", *v)
}

func statusText(code *int) string {
	if code == nil {
		return "—"
	}
	return strconv.Itoa(*code)
}

func urlQuery(s string) string {
	if len(s) > 180 {
		s = s[:180]
	}
	return url.QueryEscape(s)
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})
}
