package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// basicAuth проверяет Basic-учётку в константное время и ограничивает частоту
// неудачных попыток по IP. Для сравнения используется SHA-256-дайджест, чтобы:
//   - сравнение всегда шло на 32 байтах (длина пароля не утекает);
//   - логин сравнивался тем же числом байт, что и пароль (нет timing-утечки
//     на этапе «не тот ли логин вы прислали»).
func basicAuth(user, pass string, next http.Handler) http.Handler {
	wantUser := sha256sum(user)
	wantPass := sha256sum(pass)
	limiter := newAuthLimiter(5, 60*time.Second)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if !limiter.allow(ip) {
			w.Header().Set("Retry-After", "60")
			w.Header().Set("WWW-Authenticate", `Basic realm="webChecker"`)
			http.Error(w, "Слишком много неудачных попыток входа. Повторите через минуту.", http.StatusTooManyRequests)
			return
		}
		gotUser, gotPass, ok := r.BasicAuth()
		gotUserHash := sha256sum(gotUser)
		gotPassHash := sha256sum(gotPass)
		userMatch := subtle.ConstantTimeCompare(gotUserHash[:], wantUser[:])
		passMatch := subtle.ConstantTimeCompare(gotPassHash[:], wantPass[:])
		if !ok || userMatch&passMatch != 1 {
			limiter.fail(ip)
			w.Header().Set("WWW-Authenticate", `Basic realm="webChecker"`)
			http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
			return
		}
		limiter.success(ip)
		next.ServeHTTP(w, r)
	})
}

func sha256sum(s string) [32]byte {
	return sha256.Sum256([]byte(s))
}

// clientIP достаёт IP клиента. За reverse-proxy полезно учитывать
// X-Forwarded-For; но доверять произвольному заголовку опасно — злоумышленник
// подделает его и обойдёт rate-limit. Поэтому берём только RemoteAddr.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return strings.TrimSpace(r.RemoteAddr)
	}
	return host
}

// authLimiter — простой in-memory лимитер неудачных попыток Basic Auth.
// Храним только счётчик + метку времени на IP. При успехе счётчик обнуляется.
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

// gcLocked чистит устаревшие записи. Вызывается под mu.
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
