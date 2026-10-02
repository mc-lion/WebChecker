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
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"webchecker/internal/config"
	"webchecker/internal/models"
	"webchecker/internal/store"
	"webchecker/internal/telegram"
	webassets "webchecker/web"
)

// Pauser останавливает фоновые проверки на время операций, которые
// перезаписывают БД целиком (импорт дампа).
type Pauser interface {
	Pause(ctx context.Context) (func(), error)
}

type Server struct {
	cfg   config.Config
	store *store.Store
	tg    *telegram.Client
	sched Pauser
	pages map[string]*template.Template
}

func New(cfg config.Config, st *store.Store, tg *telegram.Client, sched Pauser) (*Server, error) {
	pages, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, store: st, tg: tg, sched: sched, pages: pages}, nil
}

// Жёсткий лимит на тело POST с формой. Формы у нас маленькие (десятки полей,
// короткие строки), 64 КБ с запасом достаточно и защищает от DoS-попыток
// надуть url.Values гигабайтами данных.
const maxFormBytes = 64 << 10

// Таймаут на обработку обычных запросов. Export/import намеренно его обходят.
const defaultHandlerTimeout = 30 * time.Second

func (s *Server) Handler() http.Handler {
	short := func(h http.HandlerFunc) http.Handler {
		return http.TimeoutHandler(h, defaultHandlerTimeout, "Таймаут обработки запроса")
	}
	shortPost := func(h http.HandlerFunc) http.Handler {
		return limitBody(short(h), maxFormBytes)
	}

	protected := http.NewServeMux()
	protected.Handle("GET /static/", staticHandler())
	protected.Handle("GET /{$}", short(s.dashboard))
	protected.Handle("GET /monitors/new", short(s.newForm))
	protected.Handle("POST /monitors", shortPost(s.create))
	protected.Handle("GET /monitors/{id}", short(s.show))
	protected.Handle("GET /monitors/{id}/edit", short(s.editForm))
	protected.Handle("GET /monitors/{id}/checks.json", short(s.checksJSON))
	protected.Handle("POST /monitors/{id}", shortPost(s.update))
	protected.Handle("POST /monitors/{id}/delete", shortPost(s.delete))
	protected.Handle("POST /monitors/{id}/toggle", shortPost(s.toggle))
	protected.Handle("GET /settings", short(s.settings))
	protected.Handle("POST /settings/telegram-test", shortPost(s.telegramTest))
	// Export/import могут выполняться минутами на крупных дампах, им короткий
	// TimeoutHandler не подходит; MaxBytesReader на import выставляется внутри.
	protected.HandleFunc("GET /settings/export", s.exportDump)
	protected.HandleFunc("POST /settings/import", s.importDump)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("/", basicAuth(s.cfg.BasicAuthUser, s.cfg.BasicAuthPassword, sameOriginOnly(protected)))
	return logging(recoverPanic(securityHeaders(s.cfg.EnableHSTS, mux)))
}

// limitBody оборачивает POST-хэндлер, ограничивая тело запроса. ParseForm при
// превышении вернёт ошибку, а writer сгенерирует 413.
func limitBody(next http.Handler, n int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, n)
		next.ServeHTTP(w, r)
	})
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
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
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
	if err := s.store.ToggleMonitorEnabled(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
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
			// С датой: на длинной истории одно время суток встречается много раз.
			T:  c.CheckedAt.Local().Format("02.01 15:04:05"),
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
		"BlockPrivateHosts":  s.cfg.BlockPrivateHosts,
	})
}

func (s *Server) telegramTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.tg.Send(ctx, "webChecker: тестовое сообщение. Telegram подключён."); err != nil {
		slog.Error("telegram test", "error", err)
		http.Redirect(w, r, "/settings?err=telegram_fail", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings?msg=telegram_ok", http.StatusSeeOther)
}

const (
	// Жёсткий предел на загружаемый дамп. Экспорт с историей легко перерастает
	// десятки мегабайт, поэтому лимит импорта должен быть заметно выше.
	maxImportBytes = 512 << 20
	// Сколько от multipart-формы держать в памяти, остальное уходит в файл.
	importMemoryBytes = 8 << 20
	// Сколько ждать завершения текущих проверок перед импортом.
	importPauseTimeout = 30 * time.Second
)

func (s *Server) exportDump(w http.ResponseWriter, r *http.Request) {
	includeChecks := r.URL.Query().Get("checks") != "0"

	suffix := "full"
	if !includeChecks {
		suffix = "monitors"
	}
	filename := fmt.Sprintf("webchecker-%s-%s.json", suffix, time.Now().Local().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)

	// Ответ пишется потоком, поэтому об ошибке на середине можно только
	// сообщить в лог: заголовки уже уехали клиенту.
	if err := s.store.StreamDump(r.Context(), w, includeChecks); err != nil {
		slog.Error("export dump", "error", err, "include_checks", includeChecks)
	}
}

func (s *Server) importDump(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBytes+1024)
	if err := r.ParseMultipartForm(importMemoryBytes); err != nil {
		slog.Warn("import: parse multipart", "error", err)
		http.Redirect(w, r, "/settings?err=import_read_fail", http.StatusSeeOther)
		return
	}
	file, header, err := r.FormFile("dump")
	if err != nil {
		http.Redirect(w, r, "/settings?err=import_no_file", http.StatusSeeOther)
		return
	}
	defer file.Close()
	if header.Size > maxImportBytes {
		http.Redirect(w, r, "/settings?err=import_too_big", http.StatusSeeOther)
		return
	}

	// Импорт удаляет monitors и checks целиком, поэтому параллельные проверки
	// нужно сначала остановить, иначе их INSERT упрётся в блокировку.
	if s.sched != nil {
		pauseCtx, cancel := context.WithTimeout(r.Context(), importPauseTimeout)
		resume, err := s.sched.Pause(pauseCtx)
		cancel()
		if err != nil {
			slog.Error("import: scheduler pause failed", "error", err)
			http.Redirect(w, r, "/settings?err=scheduler_pause", http.StatusSeeOther)
			return
		}
		defer resume()
	}

	if err := s.store.ImportStream(r.Context(), file); err != nil {
		slog.Error("import dump", "error", err)
		http.Redirect(w, r, "/settings?err=import_fail", http.StatusSeeOther)
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
		data["Error"] = errorMessage(r.URL.Query().Get("err"))
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

// errorMessage мапит код ошибки из ?err=... в локализованный текст.
// Произвольный текст в query не отображается — иначе ссылка вида
// /settings?err=Ваш+пароль+устарел... стала бы фишинговым вектором.
func errorMessage(code string) string {
	switch code {
	case "telegram_fail":
		return "Не удалось отправить тестовое сообщение. См. логи сервера."
	case "import_read_fail":
		return "Не удалось прочитать файл. Максимум 512 МБ."
	case "import_no_file":
		return "Выберите JSON-файл для импорта."
	case "import_too_big":
		return "Файл слишком большой (максимум 512 МБ)."
	case "scheduler_pause":
		return "Не удалось приостановить проверки. Повторите попытку."
	case "import_fail":
		return "Импорт не удался. См. логи сервера."
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

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusRecorder) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				slog.Error("panic in handler", "method", r.Method, "path", r.URL.Path, "panic", rec, "stack", string(debug.Stack()))
				http.Error(w, "Внутренняя ошибка сервера", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if r.URL.Path == "/healthz" || strings.HasPrefix(r.URL.Path, "/static/") {
			return
		}
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", rec.status, "duration_ms", time.Since(start).Milliseconds())
	})
}
