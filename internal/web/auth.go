package web

import (
	"crypto/subtle"
	"net/http"
)

func basicAuth(user, pass string, next http.Handler) http.Handler {
	wantUser := []byte(user)
	wantPass := []byte(pass)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(gotUser), wantUser) != 1 || subtle.ConstantTimeCompare([]byte(gotPass), wantPass) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="webChecker"`)
			http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
