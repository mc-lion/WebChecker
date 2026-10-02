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
	// Современные браузеры присылают Sec-Fetch-Site на навигационный POST.
	fetchSite := r.Header.Get("Sec-Fetch-Site")
	switch fetchSite {
	case "same-origin", "none":
		return true
	case "cross-site":
		return false
	}

	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "null" {
		// opaque origin / песочница iframe — потенциальный CSRF.
		return false
	}
	if origin == "" {
		// Нет Origin. Обычная HTML-форма в части браузеров и расширений
		// не шлёт ни Origin, ни Sec-Fetch-Site на same-origin POST.
		// Кросс-сайтовый POST Origin шлёт всегда. same-site без Origin
		// — это уже не форма с той же страницы.
		return fetchSite != "same-site"
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}
