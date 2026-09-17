package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"webchecker/internal/config"
	"webchecker/internal/telegram"
)

func TestHandlerBasicAuthAndStatic(t *testing.T) {
	srv, err := New(config.Config{
		BasicAuthUser:     "admin",
		BasicAuthPassword: "secret",
	}, nil, telegram.New(config.Config{}))
	if err != nil {
		t.Fatal(err)
	}
	handler := srv.Handler()

	unauth := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, unauth)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}

	css := httptest.NewRequest(http.MethodGet, "/static/css/app.css", nil)
	css.SetBasicAuth("admin", "secret")
	cssRec := httptest.NewRecorder()
	handler.ServeHTTP(cssRec, css)
	if cssRec.Code != http.StatusOK {
		t.Fatalf("expected css 200, got %d", cssRec.Code)
	}
	if ct := cssRec.Header().Get("Content-Type"); ct == "" {
		t.Fatal("expected content-type for css")
	}
}
