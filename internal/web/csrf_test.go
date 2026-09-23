package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSameOriginOnly(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		headers  map[string]string
		wantPass bool
	}{
		{"GET без заголовков", http.MethodGet, nil, true},
		{"POST из интерфейса", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{"POST по прямой ссылке", http.MethodPost, map[string]string{"Sec-Fetch-Site": "none"}, true},
		{"POST с чужого сайта", http.MethodPost, map[string]string{"Sec-Fetch-Site": "cross-site"}, false},
		{"POST с поддомена", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-site"}, false},
		{"POST с чужим Origin", http.MethodPost, map[string]string{"Origin": "https://evil.example"}, false},
		{"POST со своим Origin", http.MethodPost, map[string]string{"Origin": "http://webchecker.local"}, true},
		{"POST из curl", http.MethodPost, nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reached bool
			handler := sameOriginOnly(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				reached = true
			}))

			req := httptest.NewRequest(tc.method, "http://webchecker.local/monitors/1/delete", nil)
			req.Host = "webchecker.local"
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if reached != tc.wantPass {
				t.Fatalf("reached=%v, want %v (status %d)", reached, tc.wantPass, rec.Code)
			}
			if !tc.wantPass && rec.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d", rec.Code)
			}
		})
	}
}

func TestRecoverPanicReturns500(t *testing.T) {
	handler := recoverPanic(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
}

func TestUrlQueryKeepsRunesIntact(t *testing.T) {
	// Кириллица занимает 2 байта: обрезка по байтам оставляла половину символа.
	escaped := urlQuery(strings.Repeat("ф", 200))
	decoded, err := url.QueryUnescape(escaped)
	if err != nil {
		t.Fatalf("unescape failed: %v", err)
	}
	if !utf8.ValidString(decoded) {
		t.Fatalf("truncated message is not valid utf8: %q", decoded)
	}
	if got := utf8.RuneCountInString(decoded); got != 180 {
		t.Fatalf("want 180 runes, got %d", got)
	}
}
