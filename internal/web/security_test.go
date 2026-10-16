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

func TestVerifySecurityHeadersPresent(t *testing.T) {
	srv, h := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(envSessionCookie(t, srv.cfg))
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
	cfg := testAuthConfig()
	cfg.EnableHSTS = true
	srv, err := New(cfg, nil, telegram.New(config.Config{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(envSessionCookie(t, cfg))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if hsts := rec.Header().Get("Strict-Transport-Security"); hsts == "" {
		t.Fatal("HSTS должен быть выставлен при EnableHSTS=true")
	}
}

func postLogin(h http.Handler, user, pass, ip string) *httptest.ResponseRecorder {
	form := strings.NewReader("username=" + url.QueryEscape(user) + "&password=" + url.QueryEscape(pass))
	req := httptest.NewRequest(http.MethodPost, "/login", form)
	req.RemoteAddr = ip
	req.Host = "webchecker.local"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://webchecker.local")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestVerifyRateLimitAfterFiveFailures(t *testing.T) {
	_, h := newTestServer(t)
	ip := "203.0.113.5:9999"
	for i := 0; i < 5; i++ {
		rec := postLogin(h, "admin", "wrong", ip)
		if rec.Code != http.StatusOK {
			t.Fatalf("попытка %d: want 200 (форма с ошибкой), got %d", i+1, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Неверный логин") {
			t.Fatalf("попытка %d: ожидалась ошибка логина", i+1)
		}
	}
	rec := postLogin(h, "admin", "wrong", ip)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("после 5 неудач want 429, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Слишком много") {
		t.Fatalf("ожидали сообщение о rate-limit, body=%s", rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("Retry-After не выставлен")
	}
}

func TestVerifyRateLimitPerIP(t *testing.T) {
	_, h := newTestServer(t)
	for i := 0; i < 6; i++ {
		postLogin(h, "admin", "wrong", "10.0.0.1:9999")
	}
	rec := postLogin(h, "admin", "wrong", "10.0.0.2:9999")
	if strings.Contains(rec.Body.String(), "Слишком много") {
		t.Fatal("другой IP не должен быть заблокирован")
	}
	if !strings.Contains(rec.Body.String(), "Неверный логин") {
		t.Fatalf("ожидали ошибку логина, body=%s", rec.Body.String())
	}
}

func TestVerifyAuthSuccessClearsCounter(t *testing.T) {
	_, h := newTestServer(t)
	ip := "10.0.0.3:9999"
	for i := 0; i < 3; i++ {
		postLogin(h, "admin", "wrong", ip)
	}
	rec := postLogin(h, "admin", "secretpassword", ip)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("успешный login want 303, got %d", rec.Code)
	}
	for i := 0; i < 3; i++ {
		rec := postLogin(h, "admin", "wrong", ip)
		if strings.Contains(rec.Body.String(), "Слишком много") {
			t.Fatalf("попытка после сброса %d: rate-limit не должен сработать", i+1)
		}
	}
}

func TestVerifyMaxBytesOnPOST(t *testing.T) {
	srv, h := newTestServer(t)
	payload := "name=" + strings.Repeat("A", 128<<10)
	req := httptest.NewRequest(http.MethodPost, "/monitors", strings.NewReader(payload))
	req.Host = "webchecker.local"
	req.AddCookie(envSessionCookie(t, srv.cfg))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://webchecker.local")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("ожидали 400 или 413 от MaxBytesReader, got %d", rec.Code)
	}
}

func TestVerifyErrorMessageNotReflected(t *testing.T) {
	srv, h := newTestServer(t)
	payload := `<script>alert(1)</script>phishing text`
	req := httptest.NewRequest(http.MethodGet, "/settings?err="+url.QueryEscape(payload), nil)
	req.AddCookie(envSessionCookie(t, srv.cfg))
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
	srv, h := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/settings?err=telegram_fail", nil)
	req.AddCookie(envSessionCookie(t, srv.cfg))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "тестовое сообщение") {
		t.Fatal("Известный код ошибки должен отобразиться как локализованный текст")
	}
}

func TestVerifyCSRFAllowsSameOriginFormPOST(t *testing.T) {
	srv, h := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/monitors/1/delete", nil)
	req.Host = "webchecker.local"
	req.AddCookie(envSessionCookie(t, srv.cfg))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatal("same-origin форма без CSRF-заголовков не должна получать 403")
	}
}

func TestVerifyCSRFBlocksCrossSitePOST(t *testing.T) {
	srv, h := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/monitors/1/delete", nil)
	req.Host = "webchecker.local"
	req.AddCookie(envSessionCookie(t, srv.cfg))
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestVerifyLoginTimingHashed(t *testing.T) {
	_, h := newTestServer(t)
	measure := func(user, pass, ip string) time.Duration {
		start := time.Now()
		postLogin(h, user, pass, ip)
		return time.Since(start)
	}
	for i := 0; i < 10; i++ {
		measure("x", "y", "127.0.0.1:1")
	}
	var sumWrongUser, sumWrongPass time.Duration
	runs := 20
	for i := 0; i < runs; i++ {
		sumWrongUser += measure("wrong-user", "secretpassword", "10.1."+strconv.Itoa(i)+".1:1")
		sumWrongPass += measure("admin", "wrong-pass", "10.2."+strconv.Itoa(i)+".1:1")
	}
	avgU := sumWrongUser / time.Duration(runs)
	avgP := sumWrongPass / time.Duration(runs)
	t.Logf("ср. wrongUser=%s wrongPass=%s (с хешем должны быть сопоставимы)", avgU, avgP)
}

func TestJSONAuthRejectsWith401(t *testing.T) {
	_, h := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/monitors/1/checks.json", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("JSON want 401, got %d", rec.Code)
	}
}
