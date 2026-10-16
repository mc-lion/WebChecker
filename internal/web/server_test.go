package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"webchecker/internal/config"
	"webchecker/internal/telegram"
)

func testAuthConfig() config.Config {
	return config.Config{
		AuthUser:      "admin",
		AuthPassword:  "secretpassword",
		JWTSecret:     strings.Repeat("s", 32),
		JWTTTL:        24 * time.Hour,
		AuthEnvAlways: true, // без store env-сессия всегда валидна
	}
}

func newTestServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	srv, err := New(testAuthConfig(), nil, telegram.New(config.Config{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	return srv, srv.Handler()
}

func envSessionCookie(t *testing.T, cfg config.Config) *http.Cookie {
	t.Helper()
	token, exp, err := issueToken(envSubject, cfg.AuthUser, cfg.JWTSecret, cfg.JWTTTL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{
		Name:    sessionCookieName,
		Value:   token,
		Path:    "/",
		Expires: exp,
	}
}

func TestHandlerJWTAndStatic(t *testing.T) {
	srv, handler := newTestServer(t)

	unauth := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, unauth)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 to login, got %d", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/login" {
		t.Fatalf("Location=%q, want /login", loc)
	}

	css := httptest.NewRequest(http.MethodGet, "/static/css/app.css", nil)
	cssRec := httptest.NewRecorder()
	handler.ServeHTTP(cssRec, css)
	if cssRec.Code != http.StatusOK {
		t.Fatalf("expected css 200 without auth, got %d", cssRec.Code)
	}

	loginPage := httptest.NewRequest(http.MethodGet, "/login", nil)
	loginRec := httptest.NewRecorder()
	handler.ServeHTTP(loginRec, loginPage)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login page want 200, got %d", loginRec.Code)
	}

	authed := httptest.NewRequest(http.MethodGet, "/settings", nil)
	authed.AddCookie(envSessionCookie(t, srv.cfg))
	authRec := httptest.NewRecorder()
	handler.ServeHTTP(authRec, authed)
	if authRec.Code != http.StatusOK {
		t.Fatalf("authed settings want 200, got %d body=%s", authRec.Code, authRec.Body.String())
	}
}

func TestHandlerBearerToken(t *testing.T) {
	srv, handler := newTestServer(t)
	token, _, err := issueToken(envSubject, srv.cfg.AuthUser, srv.cfg.JWTSecret, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer want 200, got %d", rec.Code)
	}
}

func TestHandlerExpiredJWT(t *testing.T) {
	srv, handler := newTestServer(t)
	token, _, err := issueToken(envSubject, srv.cfg.AuthUser, srv.cfg.JWTSecret, -time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expired want 303, got %d", rec.Code)
	}
}

func TestLoginEnvCredentials(t *testing.T) {
	_, handler := newTestServer(t)
	form := strings.NewReader("username=admin&password=secretpassword")
	req := httptest.NewRequest(http.MethodPost, "/login", form)
	req.Host = "webchecker.local"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://webchecker.local")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login want 303, got %d body=%s", rec.Code, rec.Body.String())
	}
	found := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			found = true
		}
	}
	if !found {
		t.Fatal("session cookie not set")
	}
}
