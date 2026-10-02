package web

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// sameOriginOnly отклоняет изменяющие запросы, пришедшие с чужой страницы.
// Basic Auth сам по себе от CSRF не защищает: браузер подставляет сохранённые
// учётные данные и в кросс-сайтовый POST.
func sameOriginOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if safeMethod(r.Method) || allowedOrigin(r) {
			next.ServeHTTP(w, r)
			return
		}
		slog.Warn("csrf rejected",
			"method", r.Method,
			"path", r.URL.Path,
			"host", r.Host,
			"origin", r.Header.Get("Origin"),
			"sec_fetch_site", r.Header.Get("Sec-Fetch-Site"),
		)
		http.Error(w, "Запрос отклонён: проверьте, что форма отправлена из интерфейса webChecker", http.StatusForbidden)
	})
}

func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func allowedOrigin(r *http.Request) bool {
	fetchSite := r.Header.Get("Sec-Fetch-Site")
	origin := strings.TrimSpace(r.Header.Get("Origin"))

	// Origin — главный сигнал CSRF. Сверяем его раньше Sec-Fetch-Site:
	// на доступе по IP без домена Chrome часто ставит cross-site, хотя форма
	// отправлена с той же страницы.
	if origin != "" && origin != "null" {
		parsed, err := url.Parse(origin)
		if err == nil && parsed.Host != "" {
			if hostsMatch(parsed.Host, r.Host) {
				return true
			}
			if !publicWebHost(parsed.Host) {
				return true
			}
		}
	}

	switch fetchSite {
	case "same-origin", "none":
		return true
	case "cross-site":
		return false
	}

	if origin == "" || origin == "null" {
		// HTTP по IP без домена: часть браузеров шлёт Origin: null и не ставит
		// Sec-Fetch-Site. Настоящий CSRF из iframe на чужом сайте приходит с
		// Sec-Fetch-Site: cross-site / same-site.
		return fetchSite != "same-site" && fetchSite != "cross-site"
	}
	return false
}

func hostsMatch(originHost, reqHost string) bool {
	oh, op := splitHostPort(originHost)
	rh, rp := splitHostPort(reqHost)
	if !strings.EqualFold(oh, rh) {
		return false
	}
	if op == rp || op == "" || rp == "" {
		return true
	}
	return false
}

func splitHostPort(host string) (string, string) {
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		return host, ""
	}
	return h, p
}

// publicWebHost — хост, с которого реальный кросс-сайтовый CSRF возможен
// (evil.example). IP, localhost, docker-имя и *.local сюда не входят.
func publicWebHost(hostport string) bool {
	host, _ := splitHostPort(hostport)
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" || host == "localhost" {
		return false
	}
	// Любой IP — не публичный сайт. Конкретный адрес не зашиваем:
	// у контейнера он может меняться при каждом поднятии сети.
	if net.ParseIP(host) != nil {
		return false
	}
	if !strings.Contains(host, ".") {
		return false
	}
	for _, suf := range []string{".local", ".lan", ".internal", ".home", ".corp", ".intranet"} {
		if strings.HasSuffix(host, suf) {
			return false
		}
	}
	return true
}
