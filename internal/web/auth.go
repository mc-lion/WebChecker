package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"webchecker/internal/store"
)

type ctxKey int

const (
	ctxUserIDKey ctxKey = iota
	ctxUsernameKey
	ctxEnvSessionKey
)

func userIDFromContext(ctx context.Context) (int64, bool) {
	id, ok := ctx.Value(ctxUserIDKey).(int64)
	return id, ok && id > 0
}

func usernameFromContext(ctx context.Context) string {
	s, _ := ctx.Value(ctxUsernameKey).(string)
	return s
}

func isEnvSession(ctx context.Context) bool {
	v, _ := ctx.Value(ctxEnvSessionKey).(bool)
	return v
}

func withSession(ctx context.Context, userID int64, username string, env bool) context.Context {
	ctx = context.WithValue(ctx, ctxUsernameKey, username)
	ctx = context.WithValue(ctx, ctxEnvSessionKey, env)
	if userID > 0 {
		ctx = context.WithValue(ctx, ctxUserIDKey, userID)
	}
	return ctx
}

// jwtAuth проверяет JWT из cookie или Authorization: Bearer.
func (s *Server) jwtAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := extractToken(r)
		if raw == "" {
			s.authReject(w, r)
			return
		}
		claims, err := parseToken(raw, s.cfg.JWTSecret)
		if err != nil {
			s.authReject(w, r)
			return
		}

		if claims.Subject == envSubject {
			if !s.envAuthAllowed(r.Context()) {
				s.authReject(w, r)
				return
			}
			ctx := withSession(r.Context(), 0, claims.Username, true)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		id, ok := subjectUserID(claims.Subject)
		if !ok || s.store == nil {
			s.authReject(w, r)
			return
		}
		u, err := s.store.GetUser(r.Context(), id)
		if err != nil || !u.Enabled {
			s.authReject(w, r)
			return
		}
		ctx := withSession(r.Context(), u.ID, u.Username, false)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) envAuthAllowed(ctx context.Context) bool {
	if s.cfg.AuthEnvAlways {
		return true
	}
	if s.store == nil {
		return true
	}
	n, err := s.store.CountUsers(ctx)
	if err != nil {
		slog.Error("count users for env auth", "error", err)
		return false
	}
	return n == 0
}

func (s *Server) authReject(w http.ResponseWriter, r *http.Request) {
	if wantsJSON(r) {
		http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func wantsJSON(r *http.Request) bool {
	if strings.HasSuffix(r.URL.Path, ".json") {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json") && !strings.Contains(accept, "text/html")
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if extractToken(r) != "" {
		if claims, err := parseToken(extractToken(r), s.cfg.JWTSecret); err == nil {
			if claims.Subject == envSubject && s.envAuthAllowed(r.Context()) {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			if id, ok := subjectUserID(claims.Subject); ok && s.store != nil {
				if u, err := s.store.GetUser(r.Context(), id); err == nil && u.Enabled {
					http.Redirect(w, r, "/", http.StatusSeeOther)
					return
				}
			}
		}
	}
	s.render(w, r, "login.html", map[string]any{
		"Title":  "Вход",
		"Public": true,
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if s.loginLimiter != nil && !s.loginLimiter.allow(ip) {
		w.Header().Set("Retry-After", "60")
		s.renderStatus(w, r, http.StatusTooManyRequests, "login.html", map[string]any{
			"Title":  "Вход",
			"Public": true,
			"Error":  "Слишком много неудачных попыток входа. Повторите через минуту.",
		})
		return
	}
	if err := r.ParseForm(); err != nil {
		s.render(w, r, "login.html", map[string]any{
			"Title":  "Вход",
			"Public": true,
			"Error":  "Не удалось прочитать форму.",
		})
		return
	}
	username := strings.TrimSpace(r.PostForm.Get("username"))
	password := r.PostForm.Get("password")

	subject, displayName, ok := s.tryLogin(r.Context(), username, password)
	if !ok {
		if s.loginLimiter != nil {
			s.loginLimiter.fail(ip)
		}
		// Постоянное время даже при отсутствии пользователя: лишняя задержка
		// уже есть в bcrypt при DB-пути; для env сравниваем хеши.
		s.render(w, r, "login.html", map[string]any{
			"Title":    "Вход",
			"Public":   true,
			"Error":    "Неверный логин или пароль.",
			"Username": username,
		})
		return
	}
	if s.loginLimiter != nil {
		s.loginLimiter.success(ip)
	}

	token, exp, err := issueToken(subject, displayName, s.cfg.JWTSecret, s.cfg.JWTTTL)
	if err != nil {
		s.serverError(w, "issue jwt", err)
		return
	}
	setSessionCookie(w, token, exp, s.cfg.EnableHSTS)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) tryLogin(ctx context.Context, username, password string) (subject, displayName string, ok bool) {
	if username == "" || password == "" {
		return "", "", false
	}

	if s.store != nil {
		u, err := s.store.GetUserByUsername(ctx, username)
		if err == nil {
			// Имя есть в БД — только bcrypt, env не подменяет пароль.
			if !u.Enabled {
				return "", "", false
			}
			if err := store.ComparePassword(u.PasswordHash, password); err != nil {
				return "", "", false
			}
			return strconv.FormatInt(u.ID, 10), u.Username, true
		}
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("login get user", "error", err)
			return "", "", false
		}
	}

	if !s.envAuthAllowed(ctx) {
		return "", "", false
	}
	if !s.matchConfiguredEnv(username, password) {
		return "", "", false
	}
	return envSubject, s.cfg.AuthUser, true
}

func (s *Server) matchConfiguredEnv(username, password string) bool {
	wantUser := sha256sum(s.cfg.AuthUser)
	wantPass := sha256sum(s.cfg.AuthPassword)
	gotUser := sha256sum(username)
	gotPass := sha256sum(password)
	userMatch := subtle.ConstantTimeCompare(gotUser[:], wantUser[:])
	passMatch := subtle.ConstantTimeCompare(gotPass[:], wantPass[:])
	return userMatch&passMatch == 1
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	clearSessionCookie(w, s.cfg.EnableHSTS)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func sha256sum(s string) [32]byte {
	return sha256.Sum256([]byte(s))
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// authLimiter — in-memory лимитер неудачных попыток входа по IP.
type authLimiter struct {
	mu       sync.Mutex
	window   time.Duration
	max      int
	attempts map[string]*authAttempt
	lastGC   time.Time
}

type authAttempt struct {
	count int
	first time.Time
}

func newAuthLimiter(max int, window time.Duration) *authLimiter {
	return &authLimiter{
		window:   window,
		max:      max,
		attempts: make(map[string]*authAttempt),
	}
}

func (l *authLimiter) allow(ip string) bool {
	if ip == "" {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gcLocked()
	a := l.attempts[ip]
	if a == nil {
		return true
	}
	if time.Since(a.first) > l.window {
		delete(l.attempts, ip)
		return true
	}
	return a.count < l.max
}

func (l *authLimiter) fail(ip string) {
	if ip == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.attempts[ip]
	now := time.Now()
	if a == nil || now.Sub(a.first) > l.window {
		l.attempts[ip] = &authAttempt{count: 1, first: now}
		return
	}
	a.count++
}

func (l *authLimiter) success(ip string) {
	if ip == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, ip)
}

func (l *authLimiter) gcLocked() {
	now := time.Now()
	if now.Sub(l.lastGC) < l.window {
		return
	}
	l.lastGC = now
	for ip, a := range l.attempts {
		if now.Sub(a.first) > l.window {
			delete(l.attempts, ip)
		}
	}
}
