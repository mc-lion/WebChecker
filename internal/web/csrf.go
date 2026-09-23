package web

import (
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
	// Современные браузеры всегда присылают Sec-Fetch-Site.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return true
	case "":
		// Заголовка нет — разбираемся по Origin ниже.
	default:
		return false
	}

	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || origin == "null" {
		// Ни Sec-Fetch-Site, ни Origin: это не браузерная форма (curl, скрипт).
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}
