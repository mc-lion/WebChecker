package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
		{"POST same-site со своим Origin", http.MethodPost, map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "http://webchecker.local"}, true},
		{"POST с чужим Origin", http.MethodPost, map[string]string{"Origin": "https://evil.example"}, false},
		{"POST со своим Origin", http.MethodPost, map[string]string{"Origin": "http://webchecker.local"}, true},
		{"POST без заголовков — форма из интерфейса", http.MethodPost, nil, true},
		{"POST Origin null без Sec-Fetch-Site", http.MethodPost, map[string]string{"Origin": "null"}, true},
		{"POST Origin null с cross-site", http.MethodPost, map[string]string{"Origin": "null", "Sec-Fetch-Site": "cross-site"}, false},
		{"POST с IP Origin", http.MethodPost, map[string]string{"Origin": "http://192.0.2.10:8080"}, true},
		{"POST с IP Origin и cross-site меткой", http.MethodPost, map[string]string{"Origin": "http://198.51.100.20:8080", "Sec-Fetch-Site": "cross-site"}, true},
		{"POST localhost Origin", http.MethodPost, map[string]string{"Origin": "http://localhost:8080"}, true},
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

func TestSameOriginOnlyAllowsPrivateIPEvenIfHostDiffers(t *testing.T) {
	var reached bool
	handler := sameOriginOnly(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))
	req := httptest.NewRequest(http.MethodPost, "http://webchecker:8080/monitors/1", nil)
	req.Host = "webchecker:8080"
	req.Header.Set("Origin", "http://10.8.0.55:8080")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !reached {
		t.Fatalf("форма с внутреннего IP не должна получать 403 (status %d)", rec.Code)
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

func TestErrorMessageOnlyAllowsKnownCodes(t *testing.T) {
	cases := map[string]bool{
		"":                               false,
		"telegram_fail":                  true,
		"import_read_fail":               true,
		"import_no_file":                 true,
		"import_too_big":                 true,
		"scheduler_pause":                true,
		"import_fail":                    true,
		"ua_invalid":                     true,
		"ua_last":                        true,
		"<script>alert(1)</script>":      false,
		"Your+password+expired":          false,
		"../../etc/passwd":               false,
		"custom message from attacker":   false,
		"telegram_fail; drop table ...;": false,
	}
	for code, want := range cases {
		got := errorMessage(code) != ""
		if got != want {
			t.Fatalf("errorMessage(%q): got allowed=%v, want %v", code, got, want)
		}
	}
}
