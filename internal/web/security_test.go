package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"webchecker/internal/config"
	"webchecker/internal/telegram"
)

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	srv, err := New(config.Config{BasicAuthUser: "a", BasicAuthPassword: "b"}, nil, telegram.New(config.Config{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv.Handler()
}

func TestVerifySecurityHeadersPresent(t *testing.T) {
	h := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.SetBasicAuth("a", "b")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	must := map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
	}
	for name, want := range must {
		if got := rec.Header().Get(name); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp == "" {
		t.Fatal("CSP не выставлен")
	}
	if hsts := rec.Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Fatalf("HSTS не должен быть выставлен без EnableHSTS, got %q", hsts)
	}
}

func TestVerifyHSTSOnlyWithFlag(t *testing.T) {
	srv, err := New(config.Config{BasicAuthUser: "a", BasicAuthPassword: "b", EnableHSTS: true}, nil, telegram.New(config.Config{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.SetBasicAuth("a", "b")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if hsts := rec.Header().Get("Strict-Transport-Security"); hsts == "" {
		t.Fatal("HSTS должен быть выставлен при EnableHSTS=true")
	}
}

func TestVerifyRateLimitAfterFiveFailures(t *testing.T) {
	h := newTestServer(t)
	// Пять неудач — ещё 401.
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "203.0.113.5:9999"
		req.SetBasicAuth("a", "wrong")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("попытка %d: want 401, got %d", i+1, rec.Code)
		}
	}
	// Шестая — 429.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.5:9999"
	req.SetBasicAuth("a", "wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("после 5 неудач want 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After не выставлен")
	}
}

func TestVerifyRateLimitPerIP(t *testing.T) {
	h := newTestServer(t)
	// Исчерпываем лимит с одного IP.
	for i := 0; i < 6; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.1:9999"
		req.SetBasicAuth("a", "wrong")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
	}
	// Другой IP — всё ещё доступен.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:9999"
	req.SetBasicAuth("a", "wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("другой IP не должен быть заблокирован, got %d", rec.Code)
	}
}

func TestVerifyAuthSuccessClearsCounter(t *testing.T) {
	h := newTestServer(t)
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.3:9999"
		req.SetBasicAuth("a", "wrong")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	// Успех обнуляет счётчик.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.3:9999"
	req.SetBasicAuth("a", "b")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// Хэндлер / требует store, он nil — упадёт в панику → 500 через recoverPanic.
	// Нам важен только auth-уровень: не 429.
	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("rate-limit не должен сработать на верные учётки")
	}
	// Теперь снова 3 неудачи — ещё не 429.
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "10.0.0.3:9999"
		req.SetBasicAuth("a", "wrong")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("попытка после сброса %d: want 401, got %d", i+1, rec.Code)
		}
	}
}

func TestVerifyMaxBytesOnPOST(t *testing.T) {
	h := newTestServer(t)
	// 128 КБ формы — больше лимита 64 КБ.
	payload := "name=" + strings.Repeat("A", 128<<10)
	req := httptest.NewRequest(http.MethodPost, "/monitors", strings.NewReader(payload))
	req.Host = "webchecker.local"
	req.SetBasicAuth("a", "b")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://webchecker.local")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	// store=nil, но до него не должны дойти — ParseForm упадёт на лимите.
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("ожидали 400 или 413 от MaxBytesReader, got %d", rec.Code)
	}
}

func TestVerifyErrorMessageNotReflected(t *testing.T) {
	h := newTestServer(t)
	payload := `<script>alert(1)</script>phishing text`
	req := httptest.NewRequest(http.MethodGet, "/settings?err="+url.QueryEscape(payload), nil)
	req.SetBasicAuth("a", "b")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, "phishing text") {
		t.Fatal("Произвольный текст из ?err=... отразился в HTML")
	}
	if strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("HTML-escaped версия тоже не должна появиться — код неизвестен, баннер должен быть пустой")
	}
}

func TestVerifyErrorMessageKnownCodeDisplayed(t *testing.T) {
	h := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/settings?err=telegram_fail", nil)
	req.SetBasicAuth("a", "b")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "тестовое сообщение") {
		t.Fatal("Известный код ошибки должен отобразиться как локализованный текст")
	}
}

func TestVerifyCSRFAllowsSameOriginFormPOST(t *testing.T) {
	h := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/monitors/1/delete", nil)
	req.Host = "webchecker.local"
	req.SetBasicAuth("a", "b")
	// Как обычная HTML-форма: без Sec-Fetch-Site и без Origin.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatal("same-origin форма без CSRF-заголовков не должна получать 403")
	}
}

func TestVerifyCSRFBlocksCrossSitePOST(t *testing.T) {
	h := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/monitors/1/delete", nil)
	req.Host = "webchecker.local"
	req.SetBasicAuth("a", "b")
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestVerifyAuthTimingHashed(t *testing.T) {
	srv, err := New(config.Config{BasicAuthUser: "admin", BasicAuthPassword: "secret-password-long"}, nil, telegram.New(config.Config{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	measure := func(user, pass, ip string) time.Duration {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip
		req.SetBasicAuth(user, pass)
		rec := httptest.NewRecorder()
		start := time.Now()
		h.ServeHTTP(rec, req)
		return time.Since(start)
	}
	// Прогрев.
	for i := 0; i < 20; i++ {
		measure("x", "y", "127.0.0.1:1")
	}
	// 1 попытка с разных IP, чтобы не триггерить rate-limit.
	var sumWrongUser, sumWrongPass time.Duration
	runs := 20
	for i := 0; i < runs; i++ {
		sumWrongUser += measure("wrong-user", "secret-password-long", "10.1."+strconv.Itoa(i)+".1:1")
		sumWrongPass += measure("admin", "wrong-pass", "10.2."+strconv.Itoa(i)+".1:1")
	}
	avgU := sumWrongUser / time.Duration(runs)
	avgP := sumWrongPass / time.Duration(runs)
	t.Logf("ср. wrongUser=%s wrongPass=%s (с хешем должны быть сопоставимы)", avgU, avgP)
	// Жёсткого assert нет (JIT/GC шумит), только лог для глазами.
}
