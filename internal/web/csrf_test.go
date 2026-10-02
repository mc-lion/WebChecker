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
		{"POST с чужим Origin", http.MethodPost, map[string]string{"Origin": "https://evil.example"}, false},
		{"POST со своим Origin", http.MethodPost, map[string]string{"Origin": "http://webchecker.local"}, true},
		{"POST без заголовков — блокировано", http.MethodPost, nil, false},
		{"POST из curl с X-Requested-By", http.MethodPost, map[string]string{"X-Requested-By": "cli"}, true},
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

func TestErrorMessageOnlyAllowsKnownCodes(t *testing.T) {
	cases := map[string]bool{
		"":                               false,
		"telegram_fail":                  true,
		"import_read_fail":               true,
		"import_no_file":                 true,
		"import_too_big":                 true,
		"scheduler_pause":                true,
		"import_fail":                    true,
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
