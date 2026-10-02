package web

import "net/http"

// securityHeaders добавляет базовые защитные заголовки для всех ответов.
// HSTS выставляется только если фронт поднят через HTTPS: иначе браузер
// может намертво закэшировать политику и сломать доступ по http://.
func securityHeaders(hsts bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// Запрет встраивания в iframe — защита от clickjacking.
		h.Set("X-Frame-Options", "DENY")
		// Запрет MIME-sniffing: JSON-экспорт не будет интерпретирован как HTML.
		h.Set("X-Content-Type-Options", "nosniff")
		// Не утекаем URL с токенами/ID в Referer на внешние сайты.
		h.Set("Referrer-Policy", "no-referrer")
		// Запрет кросс-доменного окна для window.open-подобных атак.
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		// Минимальный CSP: всё только с нашего же origin.
		// unsafe-inline не нужен — встроенных скриптов и стилей нет.
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self'; "+
				"img-src 'self' data:; "+
				"connect-src 'self'; "+
				"font-src 'self'; "+
				"frame-ancestors 'none'; "+
				"base-uri 'self'; "+
				"form-action 'self'")
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}
